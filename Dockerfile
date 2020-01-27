FROM scratch
LABEL maintainer="squat <lserven@gmail.com>"
ARG GOARCH
COPY bin/$GOARCH/generic-device-plugin /generic-device-plugin
ENTRYPOINT ["/generic-device-plugin"]
