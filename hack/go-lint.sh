#!/bin/bash
# If CI is true, run golangci-lint directly. If we're on a developer's
# local machine, run golangci-lint from a container so we're ensuring
# a consistent environment.

set -ex

# The path the repository is mounted at in the container.
CONTAINER_REPO=/go/src/github.com/openshift-eng/openshift-tests-extension

if [ "$OPENSHIFT_CI" = "true" ];
then
  golangci-lint "${@}"
else
  DOCKER=${DOCKER:-podman}

  if ! which "$DOCKER" > /dev/null 2>&1;
  then
    echo "$DOCKER not found, please install."
    exit 1
  fi

  # Derive the repository root so this works from nested modules (e.g.
  # test/shared) as well as from the repo root: the mount point stays the
  # same, but the container workdir tracks the current directory's path
  # relative to the repo root.
  REPO_ROOT="$(git rev-parse --show-toplevel)"
  REL_PATH="$(realpath --relative-to="$REPO_ROOT" "$PWD")"
  if [ "$REL_PATH" = "." ]; then
    WORKDIR="$CONTAINER_REPO"
  else
    WORKDIR="$CONTAINER_REPO/$REL_PATH"
  fi

  # Check if running on Linux
  VOLUME_OPTION=""
  if [[ "$(uname -s)" == "Linux" ]]; then
    VOLUME_OPTION=":z"
  fi

  $DOCKER run --rm \
    --volume "${REPO_ROOT}:${CONTAINER_REPO}${VOLUME_OPTION}" \
    --workdir "$WORKDIR" \
    docker.io/golangci/golangci-lint:v2.12.2 \
    golangci-lint "${@}"
fi
