FROM docker.io/nixos/nix:2.31.1 AS builder
COPY . /tmp/build
WORKDIR /tmp/build
RUN nix \
    --extra-experimental-features "nix-command flakes" \
    --option filter-syscalls false \
    build
RUN mkdir /tmp/nix-store-closure
RUN cp -R $(nix-store -qR result/) /tmp/nix-store-closure

FROM scratch

WORKDIR /build

COPY --from=builder /tmp/nix-store-closure /nix/store
COPY --from=builder /tmp/build/result /build
ENTRYPOINT ["/build/bin/generic-device-plugin"]
