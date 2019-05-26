#!/bin/bash

# Install git
apt-get update && apt-get install -y git dnsutils

# Create sshkey for use with github
ssh-keygen -f /home/graham/.ssh/id_rsa -t rsa -b 4096 -C "graham@graz-baz" -N ""