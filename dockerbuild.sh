#!/usr/bin/env bash

docker-compose run --rm build
docker-compose build --force-rm image
