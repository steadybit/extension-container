# syntax=docker/dockerfile:1

##
## Build
##
FROM --platform=$BUILDPLATFORM golang:1.23-bookworm AS build

ARG TARGETOS TARGETARCH
ARG BUILD_WITH_COVERAGE
ARG BUILD_SNAPSHOT=true
ARG SKIP_LICENSES_REPORT=false
ARG RUNC_VERSION=v1.1.15

WORKDIR /app

RUN echo 'deb [trusted=yes] https://repo.goreleaser.com/apt/ /' > /etc/apt/sources.list.d/goreleaser.list \
    && apt-get -qq update \
    && apt-get -qq install -y --no-install-recommends build-essential libcap2-bin goreleaser gpg curl

COPY . .


RUN --mount=type=cache,target="/root/.cache/go-build" GOCACHE=/root/.cache/go-build GOOS=$TARGETOS GOARCH=$TARGETARCH goreleaser build --snapshot="${BUILD_SNAPSHOT}" --single-target -o extension \
    && setcap "cap_setuid,cap_sys_chroot,cap_setgid,cap_sys_admin,cap_dac_override+eip" ./extension

# As of today the runc binary from debian is built using golang 1.19.8 and will be flagged by CVE scanners as vulnerable to several CVEs.
# We are dowonloading the runc binary from the official github release page and will use it instead of the one from the debian package.
RUN curl -sfL https://github.com/opencontainers/runc/releases/download/$RUNC_VERSION/runc.$TARGETARCH -o ./runc \
    && curl -sfL -o - https://raw.githubusercontent.com/opencontainers/runc/refs/heads/main/runc.keyring | gpg --import \
    && curl -sfL -o - https://github.com/opencontainers/runc/releases/download/$RUNC_VERSION/runc.$TARGETARCH.asc | gpg --verify - ./runc \
    && chmod a+x ./runc

##
## Runtime
##
FROM debian:bookworm-slim

LABEL "steadybit.com.discovery-disabled"="true"

ARG USERNAME=steadybit
ARG USER_UID=10000
ARG USER_GID=$USER_UID
ARG TARGETARCH

ENV STEADYBIT_EXTENSION_RUNC_NSMOUNT_PATH="/nsmount"
ENV STEADYBIT_EXTENSION_MEMFILL_PATH="/memfill"

RUN groupadd --gid $USER_GID $USERNAME \
    && useradd --uid $USER_UID --gid $USER_GID -m $USERNAME

RUN apt-get -qq update \
    && apt-get -qq -y upgrade \
    && apt-get -qq install -y --no-install-recommends procps stress-ng iptables iproute2 dnsutils libcap2-bin util-linux cgroup-tools \
    && apt-get -y autoremove \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /run/systemd/system /sidecar

COPY --from=build /app/runc /usr/sbin/runc

USER $USER_UID

WORKDIR /

COPY --from=build /app/dist/nsmount.${TARGETARCH} /nsmount
COPY --from=build /app/dist/memfill.${TARGETARCH} /memfill
COPY --from=build /app/extension /extension
COPY --from=build /app/licenses /licenses

EXPOSE 8086 8082

ENTRYPOINT ["/extension"]
