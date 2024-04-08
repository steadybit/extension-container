# Changelog

## 1.3.13

- Update dependencies

## 1.3.12

- Update dependencies

## 1.3.11

- Update dependencies
- feat: add `host.domainname` attribute containing the host FQDN

## 1.3.10

- Update dependencies

## 1.3.9

- Update dependencies
- Pause container: action will stop if container is restarted

## 1.3.8

- Update dependencies
- Automatically set the `GOMEMLIMIT` (90% of cgroup limit) and `GOMAXPROCS`
- Disallow running mutliple tc configs on the same container

## v1.3.7


- by default ignore labels for buildpack build and lifecycle metadata
- update depencendies

## v1.3.6

- update depencendies

## v1.3.5

- update depencendies

## v1.3.4

- update depencendies

## v1.3.3

- update depencendies

## v1.3.2

- update depencendies

## v1.3.1

- Fix: don't use ipv6 when kernel module was disabled

## v1.3.0

- Stress CPU attack: cpu load percentage is based on the container limit

## v1.2.0

- Add disk fill attack
- Add timeout and recovery for container discovery
- Rework stress-io "Disk Usage" parameter to "MBytes written"

## v1.1.30

- Update dependencies

## v1.1.29

- don't follow symlinks when checking for namespace existence

## v1.1.28

- reduce discovery interval and decouple listing containers from http request

## v1.1.27

- fix: possible failed rollback of attacks for restarted containers

## v1.1.26

- fix: possible failed rollback of attacks for restarted containers

## v1.1.25

- performance improvements

## v1.1.24

- update dependencies
- added tracing for stress and network attacks
## v1.1.24

- update dependencies
- added tracing for stress and network attacks

## v1.1.23

- add pprof-endpoints

## v1.1.22

- added `DiscoveryAttributeExcludes`

## v1.1.21

- fix invalid character 'i' in literal in runc State func. Do not combine stdout and stderr for json parsing

## v1.1.20

- fix concurrent map writes in action stop

## v1.1.19

- Use overlayfs for the sidecar containers reducing cpu consumptions drastically by avoiding to extract the sidecar container over and over again

## v1.1.18

- Add canonical host.hostname attributes

## v1.1.17

- Fix regression: use separate UTS namespace when setting hostname on sidecars

## v1.1.16

- Prevent ip/tc commands being executed for the same net ns concurrently

## v1.1.15

- Add more trace logs for debugging purposes

## v1.1.14

- Only generate exclude ip/tc rules for network interfaces that are up

## v1.1.13

- avoid duplicate tc/ip rules

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
