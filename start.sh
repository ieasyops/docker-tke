#!/bin/bash
set -ex

export CONFIG_VERSION=$(git describe --tags --abbrev=0)

export IMAGES_VERSION=v1.0.1.77.g069da4cb.dirty
export CA=localhost+14.pem
export CAKey=localhost+14-key.pem

docker stack deploy -c docker-compose.yml tke 
