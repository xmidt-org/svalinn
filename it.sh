#!/usr/bin/env bash

docker --version
docker-compose --version

function svalinn-docker {
    SVALINN_VERSION=$(make version)
    make docker
}

function gungnir-docker {
    echo "Building Gungnir Image"
    git clone https://github.com/Comcast/codex-gungnir.git 2> /dev/null || true
    cd codex-gungnir
    GUNGNIR_VERSION=$(make version)
    cd ..
    printf "\n"
}

function deploy {
    echo "Deploying Cluster"
    docker swarm init
    git clone https://github.com/Comcast/codex.git 2> /dev/null || true
    pushd codex/deploy/docker-compose
    SVALINN_VERSION=$SVALINN_VERSION GUNGNIR_VERSION=$GUNGNIR_VERSION docker stack deploy codex --compose-file docker-compose.yml
    popd
    printf "\n"
}

svalinn-docker
cd ..

gungnir-docker
deploy
go get -d github.com/Comcast/codex/tests/...
printf "Starting Tests \n\n\n"
go run github.com/Comcast/codex/tests/runners/travis -feature=codex/tests/features


