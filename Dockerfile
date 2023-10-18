# syntax=docker/dockerfile:1

##
## Build
##
FROM golang:1.20-bookworm AS build

ARG BUILD_WITH_COVERAGE
ARG BUILD_SNAPSHOT=true

WORKDIR /app

RUN echo 'deb [trusted=yes] https://repo.goreleaser.com/apt/ /' > /etc/apt/sources.list.d/goreleaser.list \
    && apt-get -qq update \
    && apt-get -qq install -y --no-install-recommends build-essential libcap2-bin goreleaser

COPY go.mod ./
COPY go.sum ./
RUN go mod download

COPY . .

RUN goreleaser build --snapshot="${BUILD_SNAPSHOT}" --single-target -o extension \
    && setcap "cap_setuid,cap_setgid,cap_sys_admin,cap_dac_override+eip" ./extension

##
## Build rust
##
FROM rust:1.73-bookworm AS build-nsmount

WORKDIR /app

COPY nsmount nsmount

RUN cd nsmount && cargo build --release

##
## Runtime
##
FROM debian:bookworm-slim

LABEL "steadybit.com.discovery-disabled"="true"

ARG USERNAME=steadybit
ARG USER_UID=10000
ARG USER_GID=$USER_UID
ARG TARGETARCH

RUN groupadd --gid $USER_GID $USERNAME \
    && useradd --uid $USER_UID --gid $USER_GID -m $USERNAME

RUN apt-get -qq update \
    && apt-get -qq install -y --no-install-recommends runc libcap2-bin\
    && apt-get -y autoremove \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /run/systemd/system /sidecar

USER $USERNAME

WORKDIR /

ADD  ./sidecar_linux_$TARGETARCH.tar /sidecar
COPY --from=build /app/extension /extension
COPY --from=build /app/licenses /licenses
COPY --from=build-nsmount /app/nsmount/target/release/nsmount /usr/local/bin/nsmount

EXPOSE 8086 8082

ENTRYPOINT ["/extension"]
