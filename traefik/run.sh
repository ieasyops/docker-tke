#!/bin/bash

export CONFIG_VERSION=$(git rev-parse --short HEAD)
docker stack deploy -c docker-compose.yml traefik
