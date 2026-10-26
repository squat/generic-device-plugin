FROM --platform=$BUILDPLATFORM docker.io/nixos/nix:2.35.3@sha256:ff480dbb23511aa1c10710aa068ceea5b93fed9bfce5f5379f3a6b07282cbc7b AS builder

COPY . /tmp/build
WORKDIR /tmp/build

ARG BUILDOS
ARG BUILDARCH
ARG TARGETOS
ARG TARGETARCH
ARG VERSION

RUN VERSION="$VERSION" nix \
    --extra-experimental-features "nix-command flakes" \
    --option filter-syscalls false \
    build --impure ".#generic-device-plugin-cross-$TARGETOS-$TARGETARCH"
RUN ln -s ../bin result/bin/"$BUILDOS"_"$BUILDARCH"
FROM scratch

ARG TARGETOS
ARG TARGETARCH

COPY --from=builder /tmp/build/result/bin/"$TARGETOS"_"$TARGETARCH"/generic-device-plugin /generic-device-plugin

ENTRYPOINT ["/generic-device-plugin"]
