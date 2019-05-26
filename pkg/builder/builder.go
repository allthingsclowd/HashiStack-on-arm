package builder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	packer_common "github.com/hashicorp/packer/common"
	"github.com/hashicorp/packer/helper/config"
	"github.com/hashicorp/packer/helper/multistep"
	"github.com/hashicorp/packer/packer"
	"github.com/hashicorp/packer/template/interpolate"

	"github.com/solo-io/packer-builder-arm-image/pkg/image"
	"github.com/solo-io/packer-builder-arm-image/pkg/image/utils"
)

const BuilderId = "yuval-k.arm-image"

var knownTypes map[utils.KnownImageType][]string
var knownArgs map[utils.KnownImageType][]string

func init() {
	knownTypes = make(map[utils.KnownImageType][]string)
	knownArgs = make(map[utils.KnownImageType][]string)
	knownTypes[utils.RaspberryPi] = []string{"/boot", "/"}
	knownTypes[utils.BeagleBone] = []string{"/"}
	knownTypes[utils.Kali] = []string{"/root","/"}
	knownArgs[utils.BeagleBone] = []string{"-cpu", "cortex-a8"}
}

type Config struct {
	packer_common.PackerConfig `mapstructure:",squash"`
	// While arm image are not ISOs, we resuse the ISO logic as it basically has no ISO specific code.
	// Provide the arm image in the iso_url fields.
	packer_common.ISOConfig `mapstructure:",squash"`

	// Lets you prefix all builder commands, such as with ssh for a remote build host. Defaults to "".
	// Copied from other builders :)
	CommandWrapper string `mapstructure:"command_wrapper"`

	// Output directory, where the final image will be stored.
	OutputDir string `mapstructure:"output_directory"`

	// Image type. this is used to deduce other settings like image mounts and qemu args.
	// If not provided, we will try to deduce it from the image url. (see autoDetectType())
	ImageType utils.KnownImageType `mapstructure:"image_type"`

	// Where to mounts the image partitions in the chroot.
	// first entry is the mount point of the first partition. etc..
	ImageMounts []string `mapstructure:"image_mounts"`

	// What directories mount from the host to the chroot.
	// leave it empty for reasonable deafults.
	// array of triplets: [type, device, mntpoint].
	ChrootMounts [][]string `mapstructure:"chroot_mounts"`
	// Should the last partition be extended? this only works for the last partition in the
	// dos partition table, and ext filesystem
	LastPartitionExtraSize uint64 `mapstructure:"last_partition_extra_size"`

	// Qemu binary to use. default is qemu-arm-static
	QemuBinary string `mapstructure:"qemu_binary"`
	// Arguments to qemu binary. default depends on the image type. see init() function above.
	QemuArgs []string `mapstructure:"qemu_args"`

	ctx interpolate.Context
}

type Builder struct {
	config  Config
	runner  *multistep.BasicRunner
	context context.Context
	cancel  context.CancelFunc
}

func NewBuilder() *Builder {
	ctx, cancel := context.WithCancel(context.Background())
	return &Builder{
		context: ctx,
		cancel:  cancel,
	}
}

func (b *Builder) autoDetectType() utils.KnownImageType {
	if len(b.config.ISOUrls) < 1 {
		return ""
	}
	url := b.config.ISOUrls[0]
	return utils.GuessImageType(url)
}

func (b *Builder) Prepare(cfgs ...interface{}) ([]string, error) {
	err := config.Decode(&b.config, &config.DecodeOpts{
		Interpolate:       true,
		InterpolateFilter: &interpolate.RenderFilter{},
	}, cfgs...)
	if err != nil {
		return nil, err
	}
	var errs *packer.MultiError
	var warnings []string
	isoWarnings, isoErrs := b.config.ISOConfig.Prepare(&b.config.ctx)
	warnings = append(warnings, isoWarnings...)
	errs = packer.MultiErrorAppend(errs, isoErrs...)

	if b.config.OutputDir == "" {
		b.config.OutputDir = fmt.Sprintf("output-%s", b.config.PackerConfig.PackerBuildName)
	}

	if b.config.ChrootMounts == nil {
		b.config.ChrootMounts = make([][]string, 0)
	}

	if len(b.config.ChrootMounts) == 0 {
		b.config.ChrootMounts = [][]string{
			{"proc", "proc", "/proc"},
			{"sysfs", "sysfs", "/sys"},
			{"bind", "/dev", "/dev"},
			{"devpts", "devpts", "/dev/pts"},
			{"binfmt_misc", "binfmt_misc", "/proc/sys/fs/binfmt_misc"},
		}
	}

	if b.config.CommandWrapper == "" {
		b.config.CommandWrapper = "{{.Command}}"
	}

	if b.config.ImageType == "" {
		// defaults...
		b.config.ImageType = b.autoDetectType()
	} else {
		if _, ok := knownTypes[b.config.ImageType]; !ok {

			var validvalues []utils.KnownImageType
			for k := range knownTypes {
				validvalues = append(validvalues, k)
			}
			errs = packer.MultiErrorAppend(errs, fmt.Errorf("unknown image_type. must be one of: %v", validvalues))
			b.config.ImageType = ""
		}
	}
	if b.config.ImageType != "" {
		if len(b.config.ImageMounts) == 0 {
			b.config.ImageMounts = knownTypes[b.config.ImageType]
		}
		if len(b.config.QemuArgs) == 0 {
			b.config.QemuArgs = knownArgs[b.config.ImageType]
		}
	}

	if len(b.config.ImageMounts) == 0 {
		errs = packer.MultiErrorAppend(errs, fmt.Errorf("no image mounts provided. Please set the image mounts or image type."))
	}

	if b.config.QemuBinary == "" {
		b.config.QemuBinary = "qemu-arm-static"
	}
	// convert to full path
	path, err := exec.LookPath(b.config.QemuBinary)
	if err != nil {
		errs = packer.MultiErrorAppend(errs, fmt.Errorf("qemu binary not found."))
	} else {
		if !strings.Contains(path, "qemu-") {
			warnings = append(warnings, "binary doesn't look like qemu-user")
		}
		b.config.QemuBinary = path
	}

	if errs != nil && len(errs.Errors) > 0 {
		return warnings, errs
	}
	return warnings, nil
}

type wrappedCommandTemplate struct {
	Command string
}

func (b *Builder) Run(ui packer.Ui, hook packer.Hook, cache packer.Cache) (packer.Artifact, error) {

	wrappedCommand := func(command string) (string, error) {
		ctx := b.config.ctx
		ctx.Data = &wrappedCommandTemplate{Command: command}
		return interpolate.Render(b.config.CommandWrapper, &ctx)
	}

	state := new(multistep.BasicStateBag)
	state.Put("cache", cache)
	state.Put("config", &b.config)
	state.Put("debug", b.config.PackerDebug)
	state.Put("hook", hook)
	state.Put("ui", ui)
	state.Put("wrappedCommand", CommandWrapper(wrappedCommand))

	steps := []multistep.Step{
		&packer_common.StepDownload{
			Checksum:     b.config.ISOChecksum,
			ChecksumType: b.config.ISOChecksumType,
			Description:  "Image",
			ResultKey:    "iso_path",
			Url:          b.config.ISOUrls,
			Extension:    b.config.TargetExtension,
			TargetPath:   b.config.TargetPath,
		},
		&stepCopyImage{FromKey: "iso_path", ResultKey: "imagefile", ImageOpener: image.NewImageOpener(ui)},
	}

	if b.config.LastPartitionExtraSize > 0 {
		steps = append(steps,
			&stepResizeLastPart{FromKey: "imagefile"},
		)
	}

	steps = append(steps,
		&stepMapImage{ImageKey: "imagefile", ResultKey: "partitions"},
	)
	if b.config.LastPartitionExtraSize > 0 {
		steps = append(steps,
			&stepResizeFs{PartitionsKey: "partitions"},
		)
	}

	steps = append(steps,
		&stepMountImage{PartitionsKey: "partitions", ResultKey: "mount_path"},
		&StepMountExtra{ChrootKey: "mount_path"},
		&stepQemuUserStatic{ChrootKey: "mount_path", PathToQemuInChrootKey: "qemuInChroot", Args: Args{Args: b.config.QemuArgs}},
		&stepRegisterBinFmt{QemuPathKey: "qemuInChroot"},
		&StepChrootProvision{ChrootKey: "mount_path"},
	)

	b.runner = &multistep.BasicRunner{Steps: steps}

	done := make(chan struct{})

	go func() {
		select {
		case <-done:
			return
		case <-b.context.Done():
			b.runner.Cancel()
			hook.Cancel()
		}
	}()

	// Executes the steps
	b.runner.Run(state)
	close(done)

	if rawErr, ok := state.GetOk("error"); ok {
		return nil, rawErr.(error)
	}
	// check if it is ok
	_, canceled := state.GetOk(multistep.StateCancelled)
	_, halted := state.GetOk(multistep.StateHalted)
	if canceled || halted {
		return nil, errors.New("step canceled or halted")
	}

	return &Artifact{image: state.Get("imagefile").(string)}, nil
}

func (b *Builder) Cancel() {
	if b.runner != nil {
		b.cancel()
	}
}

type Artifact struct {
	image string
}

func (a *Artifact) BuilderId() string {
	return BuilderId
}

func (a *Artifact) Files() []string {
	return []string{a.image}
}

func (a *Artifact) Id() string {
	return ""
}

func (a *Artifact) String() string {
	return a.image
}

func (a *Artifact) State(name string) interface{} {
	return nil
}

func (a *Artifact) Destroy() error {
	return os.Remove(a.image)
}
