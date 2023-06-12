# syntax=docker/dockerfile:1

##
## Build
##
FROM golang:1.20-bullseye AS build

ARG NAME
ARG VERSION
ARG REVISION

WORKDIR /app

RUN apt-get -qq update \
    && apt-get -qq install -y --no-install-recommends build-essential libcap2-bin

COPY go.mod ./
COPY go.sum ./
RUN go mod download

COPY . .

RUN go build \
    -ldflags="\
    -X 'github.com/steadybit/extension-kit/extbuild.ExtensionName=${NAME}' \
    -X 'github.com/steadybit/extension-kit/extbuild.Version=${VERSION}' \
    -X 'github.com/steadybit/extension-kit/extbuild.Revision=${REVISION}'" \
    -o ./extension \
    main.go \
    && setcap "cap_setuid,cap_setgid,cap_sys_admin,cap_dac_override+eip" ./extension

##
## Runtime
##
FROM debian:bullseye-slim

ARG USERNAME=steadybit
ARG USER_UID=10000
ARG USER_GID=$USER_UID
ARG TARGETARCH

RUN groupadd --gid $USER_GID $USERNAME \
    && useradd --uid $USER_UID --gid $USER_GID -m $USERNAME

RUN apt-get -qq update \
    && apt-get -qq install -y --no-install-recommends runc libcap2-bin\
    && apt-get -y autoremove \
    && rm -rf /var/lib/apt/lists/*

USER $USERNAME

WORKDIR /

COPY  ./sidecar_linux_$TARGETARCH.tar /sidecar.tar
COPY --from=build /app/extension /extension

EXPOSE 8086

ENTRYPOINT ["/extension"]
