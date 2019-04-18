#!/usr/bin/env bash

docker --version
docker-compose --version

function check() {
  if [[ $1 -ne 0 ]] ; then
      exit 1
  fi
}

function svalinn-docker {
    SVALINN_VERSION="$(make version -s)"
    make docker
    check $?
}

function deploy {
    echo "Deploying Cluster"
    git clone https://github.com/Comcast/codex.git 2> /dev/null || true
    pushd codex/deploy/docker-compose
    SVALINN_VERSION=$SVALINN_VERSION docker-compose up -d db db-init svalinn
    check $?
    popd
    printf "\n"
}

svalinn-docker
cd ..

echo "Svalinn V:$SVALINN_VERSION"
deploy
go get -d github.com/Comcast/codex/tests/...
printf "Starting Tests \n\n\n"
go run github.com/Comcast/codex/tests/runners/travis -feature=codex/tests/features/svalinn/travis
check $?
