# Changelog

## v1.1.12

- update dependencies

## v1.1.10

- fix: reverting network attacks was mistakenly skipped after pid rollover happened

## v1.1.9

- ignore container with label `steadybit.com.discovery-disabled"="true"` during discovery

## v1.1.8

- update dependencies
- ignore marked containers during discovery
- migration to new unified steadybit actionIds and targetTypes

## v1.1.7

- Add mode for stress io attack to choose between read/write and/or flush stress

## v1.1.6

- update dependencies

## v1.1.5

- Don't spam the log with missing container warnings on containerd

## v1.1.4

- Exclude not-running containerd container from discovery

## v1.1.3

- Exclude pause containers from Kubernetes and ECS in discovery
- Fix error for runc inspecting containers using the systemd cgroup manager

## v1.1.2

- fix rpm dependencies

## v1.1.1

- add support for unix domain sockets
- build linux packages

## v1.1.0

 - prefix container labels with `container.`

## v1.0.3

 - Bugfix: Blackhole and DNS container isn't reverted properly when container failed (and not the pod)

## v1.0.2

 - New: new container.image attributes for registry, repository, and tag
 - Improvement: Logging improved when container couldn't stop because it wasn't found
 - Improvement: Error message for failures when starting stress-ng attacks
 - Bugfix: Fixed unique container ids for sidecar containers in same pod
 - Bugfix: Removing trailing / in container.name
 - Bugfix: Datatype for stop-container's `graceful` parameter
 - Bugfix: Blackhole container isn't reverted properly when container failed (and not the pod)

## v1.0.1

 - Bugfixes
 - Conflicting ports when using with extension-host

## v1.0.0

 - Initial release
