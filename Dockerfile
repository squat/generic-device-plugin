FROM --platform=$BUILDPLATFORM docker.io/nixos/nix:2.31.1 AS builder

COPY . /tmp/build
WORKDIR /tmp/build

ARG BUILDOS
ARG BUILDARCH
ARG TARGETOS
ARG TARGETARCH

RUN nix \
    --extra-experimental-features "nix-command flakes" \
    --option filter-syscalls false \
    build ".#generic-device-plugin-cross-$TARGETOS-$TARGETARCH"
RUN ln -s ../bin result/bin/"$BUILDOS"_"$BUILDARCH"
FROM scratch

ARG TARGETOS
ARG TARGETARCH

COPY --from=builder /tmp/build/result/bin/"$TARGETOS"_"$TARGETARCH"/generic-device-plugin /generic-device-plugin

ENTRYPOINT ["/generic-device-plugin"]
