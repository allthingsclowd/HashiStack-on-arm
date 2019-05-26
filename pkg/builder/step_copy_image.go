package builder

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/solo-io/packer-builder-arm-image/pkg/image"
	"github.com/solo-io/packer-builder-arm-image/pkg/utils"

	"github.com/hashicorp/packer/helper/multistep"
	"github.com/hashicorp/packer/packer"
)

type stepCopyImage struct {
	FromKey, ResultKey string
	ImageOpener        image.ImageOpener
	ui                 packer.Ui
}

func (s *stepCopyImage) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	fromFile := state.Get(s.FromKey).(string)
	config := state.Get("config").(*Config)
	s.ui = state.Get("ui").(packer.Ui)
	s.ui.Say("Copying source image.")

	imageName := "image"

	dstfile := filepath.Join(config.OutputDir, imageName)
	err := s.copy(ctx, state, fromFile, config.OutputDir, imageName)
	if err != nil {
		s.ui.Error(fmt.Sprintf("%v", err))
		return multistep.ActionHalt
	}

	state.Put(s.ResultKey, dstfile)
	return multistep.ActionContinue
}

func (s *stepCopyImage) Cleanup(state multistep.StateBag) {
}

func (s *stepCopyImage) copy_progress(ctx context.Context, state multistep.StateBag, dst io.Writer, src image.Image) error {
	ui := state.Get("ui").(packer.Ui)

	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {

			case <-time.After(time.Second):
				if _, ok := state.GetOk(multistep.StateCancelled); ok {
					ui.Say("Interrupt received. Cancelling copy...")
					break
				}
			case <-done:
				return
			}
		}
		cancel()
	}()

	_, err := utils.CopyWithProgress(ctx, ui, dst, src)
	return err

}

func (s *stepCopyImage) copy(ctx context.Context, state multistep.StateBag, src, dir, filename string) error {

	srcf, err := s.ImageOpener.Open(src)
	if err != nil {
		return err
	}
	defer srcf.Close()

	err = os.MkdirAll(dir, 0755)
	if err != nil {
		return err
	}

	dstf, err := os.Create(filepath.Join(dir, filename))
	if err != nil {
		return err
	}
	defer dstf.Close()

	err = s.copy_progress(ctx, state, dstf, srcf)

	if err != nil {
		return err
	}

	return nil
}
