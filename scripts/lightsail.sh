#!/bin/bash

sudo apt-get update -y &&
sudo apt-get install -y curl docker.io
if [[ $? -ne 0 ]]; then echo "Install curl failed."; exit 1; fi

rm -f linux-oryx-en.tar.gz &&
curl -O -fsSL https://github.com/ossrs/oryx/releases/latest/download/linux-oryx-en.tar.gz
if [[ $? -ne 0 ]]; then echo "Download oryx failed."; exit 1; fi

tar xf linux-oryx-en.tar.gz
if [[ $? -ne 0 ]]; then echo "Unpack oryx failed."; exit 1; fi

sed -i 's|MGMT_PORT=2022|MGMT_PORT=80|g' oryx/mgmt/bootstrap &&
sed -i 's|HTTPS_PORT=2443|HTTPS_PORT=443|g' oryx/mgmt/bootstrap
if [[ $? -ne 0 ]]; then echo "Update bootstrap failed."; exit 1; fi

sudo bash oryx/scripts/setup-ubuntu/install.sh
if [[ $? -ne 0 ]]; then echo "Install oryx failed."; exit 1; fi
