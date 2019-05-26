#!/bin/bash

# Install golang 1.11.4 and configure bashrc

wget https://storage.googleapis.com/golang/go1.11.4.linux-armv6l.tar.gz
sudo tar -C /usr/local -xvf go1.11.4.linux-armv6l.tar.gz
cat >> /home/graham/.bashrc << 'EOF'
export GOPATH=$HOME/go
export PATH=/usr/local/go/bin:$PATH:$GOPATH/bin
EOF