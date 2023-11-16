# syntax=docker/dockerfile:1

##
## Build
##
FROM --platform=$BUILDPLATFORM golang:1.21-bookworm AS build

ARG TARGETOS TARGETARCH
ARG BUILD_WITH_COVERAGE
ARG BUILD_SNAPSHOT=true

WORKDIR /app

RUN echo 'deb [trusted=yes] https://repo.goreleaser.com/apt/ /' > /etc/apt/sources.list.d/goreleaser.list \
    && apt-get -qq update \
    && apt-get -qq install -y --no-install-recommends build-essential libcap2-bin goreleaser

COPY . .

RUN GOOS=$TARGETOS GOARCH=$TARGETARCH goreleaser build --snapshot="${BUILD_SNAPSHOT}" --single-target -o extension \
    && setcap "cap_setuid,cap_sys_chroot,cap_setgid,cap_sys_admin,cap_dac_override+eip" ./extension
##
## Runtime
##
FROM debian:bookworm-slim

LABEL "steadybit.com.discovery-disabled"="true"

ARG USERNAME=steadybit
ARG USER_UID=10000
ARG USER_GID=$USER_UID
ARG TARGETARCH

ENV STEADYBIT_EXTENSION_NSMOUNT_PATH="/nsmount"

RUN groupadd --gid $USER_GID $USERNAME \
    && useradd --uid $USER_UID --gid $USER_GID -m $USERNAME

RUN apt-get -qq update \
    && apt-get -qq install -y --no-install-recommends runc libcap2-bin \
    && apt-get -y autoremove \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /run/systemd/system /sidecar

USER $USERNAME

WORKDIR /

ADD  ./sidecar_linux_$TARGETARCH.tar /sidecar
COPY ./nsmount/target/${TARGETARCH}-unknown-linux-gnu/release/nsmount /nsmount
COPY --from=build /app/extension /extension
COPY --from=build /app/licenses /licenses

EXPOSE 8086 8082

ENTRYPOINT ["/extension"]
