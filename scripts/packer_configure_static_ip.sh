#!/bin/bash

# Configure static ip addresses

cat >> /etc/dhcpcd.conf <<EOF
interface eth0

static ip_address=192.168.1.${OCTET}/24
static routers=192.168.1.1
static domain_name_servers=8.8.8.8

interface wlan0

static ip_address=192.168.86.${OCTET}/24
static routers=192.168.86.1
static domain_name_servers=8.8.8.8
EOF