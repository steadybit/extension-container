<img src="./logo.svg" height="130" align="right" alt="Container logo">

# Steadybit extension-container

This [Steadybit](https://www.steadybit.com/) extension provides a container discovery and the various actions for container targets.

Learn about the capabilities of this extension in
our [Reliability Hub](https://hub.steadybit.com/extension/com.steadybit.extension_container).

## Configuration

| Environment Variable                                | Helm value                                                         | Meaning                                                                                                                    | Required | Default |
|-----------------------------------------------------|--------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------|----------|---------|
| `STEADYBIT_EXTENSION_CONTAINER_RUNTIME`             | `container.runtime`                                                | The container runtime to user either `docker`, `containerd` or `cri-o`. Will be automatically configured if not specified. | yes      | (auto)  |
| `STEADYBIT_EXTENSION_CONTAINER_SOCKET`              | `containerRuntimes.(docker/containerd/cri-o).socket`               | The socket used to connect to the container runtime. Will be automatically configured if not specified.                    | yes      | (auto)  |
| `STEADYBIT_EXTENSION_CONTAINERD_NAMESPACE`          |                                                                    | The containerd namespace to use.                                                                                           | yes      | k8s.io  |
| `STEADYBIT_EXTENSION_OCIRUNTIME_EXECUTABLE`         | `containerRuntimes.(docker/containerd/cri-o).ociruntimeExectuable` | The OCI runtime to use (`runc` or `crun`).                                                                                 | yes      | (auto)  |
| `STEADYBIT_EXTENSION_OCIRUNTIME_ROOT`               | `containerRuntimes.(docker/containerd/cri-o).ociruntimeRoot`       | The OCI runtime root to use.                                                                                               | yes      | (auto)  |
| `STEADYBIT_EXTENSION_OCIRUNTIME_DEBUG`              |                                                                    | Activate debug mode for OCI runtime.                                                                                       | yes      | k8s.io  |
| `STEADYBIT_EXTENSION_OCIRUNTIME_ROOTLESS`           |                                                                    | Set value for OCI runtime --rootless parameter                                                                             | yes      | k8s.io  |
| `STEADYBIT_EXTENSION_OCIRUNTIME_SYSTEMD_CGROUP`     |                                                                    | Set value for OCI runtime --systemd-cgroup parameter                                                                       | yes      | k8s.io  |
| `STEADYBIT_EXTENSION_DISCOVERY_CALL_INTERVAL`       |                                                                    | Interval for container discovery                                                                                           | false    | `30s`   |
| `STEADYBIT_EXTENSION_DISABLE_DISCOVERY_EXCLUDES`    | `discovery.disableExcludes`                                        | Ignore discovery excludes specified by `steadybit.com/discovery-disabled`                                                  | false    | `false` |
| `STEADYBIT_EXTENSION_DISCOVERY_ATTRIBUTES_EXCLUDES` | `discovery.attributes.excludes`                                    | List of Target Attributes which will be excluded during discovery. Checked by key equality and supporting trailing "*"     | false    |         |
| `STEADYBIT_EXTENSION_HOSTNAME`                      |                                                                    | Optional hostname for the targets to be reported. If not given will be read from the UTS namespace of the init process     | false    |         |

The extension supports all environment variables provided
by [steadybit/extension-kit](https://github.com/steadybit/extension-kit#environment-variables).

When installed as linux package this configuration is in`/etc/steadybit/extension-container`.

## Needed capabilities

The capabilities needed by this extension are: (which are provided by the helm chart)

- `SYS_ADMIN`
- `SYS_CHROOT`
- `SYS_PTRACE`
- `NET_ADMIN`
- `NET_BIND_SERVICE`
- `DAC_OVERRIDE`
- `SETUID`
- `SETGID`
- `KILL`
- `AUDIT_WRITE`

Optional:

- `SYS_RESOURCE`

## Installation

### Kubernetes

Detailed information about agent and extension installation in kubernetes can also be found in
our [documentation](https://docs.steadybit.com/install-and-configure/install-agent/install-on-kubernetes).

#### Recommended (via agent helm chart)

All extensions provide a helm chart that is also integrated in the
[helm-chart](https://github.com/steadybit/helm-charts/tree/main/charts/steadybit-agent) of the agent.

The extension is installed by default when you install the agent.

You can provide additional values to configure this extension.

```
--set extension-container.container.runtime=containerd \
```

Additional configuration options can be found in
the [helm-chart](https://github.com/steadybit/extension-container/blob/main/charts/steadybit-extension-container/values.yaml)
of the
extension.

#### Alternative (via own helm chart)

If you need more control, you can install the extension via its
dedicated [helm-chart](https://github.com/steadybit/extension-container/blob/main/charts/steadybit-extension-container).

```bash
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

### Linux Package

Please use
our [agent-linux.sh script](https://docs.steadybit.com/install-and-configure/install-agent/install-on-linux-hosts)
to install the extension on your Linux machine. The script will download the latest version of the extension and install
it using the package manager.

After installing, configure the extension by editing `/etc/steadybit/extension-container` and then restart the service.

## Extension registration

Make sure that the extension is registered with the agent. In most cases this is done automatically. Please refer to
the [documentation](https://docs.steadybit.com/install-and-configure/install-agent/extension-registration) for more
information about extension registration and how to verify.

## Security

We try to limit the access needed for the extension to the absolute minimum. So the extension itself can run as a
non-root user on a read-only root file-system and will, by default, if deployed using the provided helm chart.

In order to execute certain actions the extension needs extended capabilities, see details below.

### Discovery / State attacks

For discovery and executing state attacks, such as stop or pause container, the extension needs access to the container
runtime socket.

### Resource Attacks

The resource attacks are starting processes in the target containers cgroup/namespaces using [runc (APL2.0)](https://github.com/opencontainers/runc) for this the following capabilities are needed: `CAP_SYS_CHROOT`, `CAP_SYS_ADMIN`, `CAP_SYS_PTRACE`, `CAP_NET_BIND_SERVICE`, `CAP_DAC_OVERRIDE`, `CAP_SETUID`, `CAP_SETGID`, `CAP_AUDIT_WRITE`, `CAP_KILL`.
These processes are executed with the root user, but are short-lived and terminated after the attack is finished.

The resource attacks optionally need `CAP_SYS_RESOURCE`. We'd recommend it to be used, otherwise the resource attacks are more likely to be oom-killed by the kernel and fail to carry out the attack.

Under the hood [stress-ng (GPL2.0)](https://github.com/ColinIanKing/stress-ng) is used to perform the stress attacks.
For the fill disk `dd` or `fallocate`  and [nsmount (MIT)](https://github.com/steadybit/nsmount) is used.
For the fill memory [memfill (MIT)](https://github.com/steadybit/memfill) is used.

All needed binaries are included in the extension container image.

### Network Attacks

The network attacks are starting processes in the target containers network namespaces using [runc (APL2.0)](https://github.com/opencontainers/runc) for this the following capabilities are needed: `CAP_NET_ADMIN`,  `CAP_SYS_CHROOT`, `CAP_SYS_ADMIN`, `CAP_SYS_PTRACE`, `CAP_NET_BIND_SERVICE`, `CAP_DAC_OVERRIDE`, `CAP_SETUID`, `CAP_SETGID`, `CAP_AUDIT_WRITE`, `CAP_KILL`.
These processes are executed with the root user, but are short-lived and terminated after the attack is finished.

Under the hood start `ip` or `tc` is used to reconfigure the network stack and `dig` is used in case the hostnames need to be resolved.

All needed binaries are included in the extension container image.

### Mark resources as "do not discover"

to exclude container from discovery you can add the label `LABEL "steadybit.com.discovery-disabled"="true"` to the
container Dockerfile.

## Troubleshooting

Using cgroups v2 on the host and `nsdelegate` to mount the cgroup filesystem will prevent
the action from running processes in other cgroups (e.g. stress cpu/memory, disk fill).
In this case you need to remount the cgroup filesystem without the `nsdelegate` option.

```sh
sudo mount -o remount,rw,nosuid,nodev,noexec,relatime -t cgroup2 none /sys/fs/cgroup
```

## Version and Revision

The version and revision of the extension:
- are printed during the startup of the extension
- are added as a Docker label to the image
- are available via the `version.txt`/`revision.txt` files in the root of the image
