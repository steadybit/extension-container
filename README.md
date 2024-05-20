<img src="./logo.svg" height="130" align="right" alt="Container logo">

# Steadybit extension-container

This [Steadybit](https://www.steadybit.com/) extension provides a host discovery and the various actions for container
targets.

Learn about the capabilities of this extension in
our [Reliability Hub](https://hub.steadybit.com/extension/com.steadybit.extension_container).

## Configuration

| Environment Variable                                | Helm value                                             | Meaning                                                                                                                    | Required | Default |
|-----------------------------------------------------|--------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------|----------|---------|
| `STEADYBIT_EXTENSION_CONTAINER_RUNTIME`             | `container.runtime`                                    | The container runtime to user either `docker`, `containerd` or `cri-o`. Will be automatically configured if not specified. | yes      | (auto)  |
| `STEADYBIT_EXTENSION_CONTAINER_SOCKET`              | `containerRuntimes.(docker/containerd/cri-o).socket`   | The socket used to connect to the container runtime. Will be automatically configured if not specified.                    | yes      | (auto)  |
| `STEADYBIT_EXTENSION_CONTAINERD_NAMESPACE`          |                                                        | The containerd namespace to use.                                                                                           | yes      | k8s.io  |
| `STEADYBIT_EXTENSION_RUNC_ROOT`                     | `containerRuntimes.(docker/containerd/cri-o).runcRoot` | The runc root to use.                                                                                                      | yes      | (auto)  |
| `STEADYBIT_EXTENSION_RUNC_DEBUG`                    |                                                        | Activate debug mode for runc.                                                                                              | yes      | k8s.io  |
| `STEADYBIT_EXTENSION_RUNC_ROOTLESS`                 |                                                        | Set value for runc --rootless parameter                                                                                    | yes      | k8s.io  |
| `STEADYBIT_EXTENSION_RUNC_SYSTEMD_CGROUP`           |                                                        | Set value for runc --systemd-cgroup parameter                                                                              | yes      | k8s.io  |
| `STEADYBIT_EXTENSION_DISCOVERY_CALL_INTERVAL`       |                                                        | Interval for container discovery                                                                                           | false    | `30s`   |
| `STEADYBIT_EXTENSION_DISABLE_DISCOVERY_EXCLUDES`    | `discovery.disableExcludes`                            | Ignore discovery excludes specified by `steadybit.com/discovery-disabled`                                                  | false    | `false` |
| `STEADYBIT_EXTENSION_DISCOVERY_ATTRIBUTES_EXCLUDES` | `discovery.attributes.excludes`                        | List of Target Attributes which will be excluded during discovery. Checked by key equality and supporting trailing "*"     | false    |         |

The extension supports all environment variables provided by [steadybit/extension-kit](https://github.com/steadybit/extension-kit#environment-variables).

When installed as linux package this configuration is in`/etc/steadybit/extension-container`.

## Needed capabilities

The capabilities needed by this extension are: (which are provided by the helm chart)

- SYS_ADMIN
- SYS_CHROOT
- SYS_RESOURCE
- SYS_PTRACE
- KILL
- NET_ADMIN
- DAC_OVERRIDE
- SETUID
- SETGID
- AUDIT_WRITE

## Installation

### Using Helm in Kubernetes

```sh
helm repo add steadybit-extension-container https://steadybit.github.io/extension-container
helm repo update
helm upgrade steadybit-extension-container \
    --install \
    --wait \
    --timeout 5m0s \
    --create-namespace \
    --namespace steadybit-agent \
    --set container.runtime=docker \
    steadybit-extension-container/steadybit-extension-container
```

### Using Docker

This extension is by default deployed using
our [agent.sh docker compose script](https://docs.steadybit.com/install-and-configure/install-agent/install-as-docker-container).

Or you can run it manually:

```sh
docker run \
  --rm \
  -p 8086 \
  --privileged \
  --pid=host \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /run/docker/runtime-runc/moby:/run/docker/runtime-runc/moby\
  -v /sys/fs/cgroup:/sys/fs/cgroup\
  --name steadybit-extension-container \
  ghcr.io/steadybit/extension-container:latest
```

### Linux Package

Please use our [agent-linux.sh script](https://docs.steadybit.com/install-and-configure/install-agent/install-on-linux-hosts) to install the
extension on your Linux machine.
The script will download the latest version of the extension and install it using the package manager.

## Register the extension

Make sure to register the extension at the steadybit platform. Please refer to
the [documentation](https://docs.steadybit.com/integrate-with-steadybit/extensions/extension-installation) for more
information.

## Anatomy of the extension / Security

We try to limit the needed access needed for the extension to the absolute minimum. So the extension itself can run as a
non-root user on a read-only root file-system and will by default if deployed using the provided helm-chart.
In order do execute certain actions the extension needs certain capabilities.

### discovery / state attacks

For discovery and executing state attacks such as stop or pause container the extension needs access to the container
runtime socket.

### resource and network attacks

Resource attacks starting stress-ng processes, the network attacks are starting ip or tc processes as runc container
reusing the target container's linux namespace(s), control group(s) and user.
This requires the following capabilities: SYS_CHROOT, SYS_ADMIN, SYS_RESOURCE, SYS_PTRACE, KILL, NET_ADMIN, DAC_OVERRIDE, SETUID,
SETGID, AUDIT_WRITE.
The needed binaries are included in the extension container image.

### mark resources as "do not discover"

to exclude container from discovery you can add the label `LABEL "steadybit.com.discovery-disabled"="true"` to the container Dockerfile.

## Troubleshooting

When the host is using cgorups v2 and the cgroup filesystem is mounted using the `nsdelegate` option will prevent that the action running processces in other cgroups (e.g. stress cpu/memory, disk fill) will fail.
In that case you need to remount the cgroup filesystem without the `nsdelegate` option.

```sh
sudo mount -o remount,rw,nosuid,nodev,noexec,relatime -t cgroup2 none /sys/fs/cgroup
```
