# Changelog

## v1.5.11

- Fill Disk: validate permissions of target directory

## v1.5.10

- Handle OOMs in fill disk attack
- Show summary message when container is stopped during stress and fill actions
- Await fill disk attack duration before removing the created file
- Don't fail resource attacks if target container is gone
- Fix flaky schedule utils test
- Update dependencies

## v1.5.9

- Update dependencies

## v1.5.8

- Update dependencies

## v1.5.7

- Update dependencies (runc and crun)

## v1.5.6

- Update dependencies

## v1.5.5

## v1.5.4

- feat: Network Delay - add option "TCP Data Packets Only" (PSH heuristic). Uses iptables marks + tc fwmark to delay only TCP data packets; UDP is not delayed. Honors ports/hosts/CIDRs via iptables filtering.
- chore: debug logging prints prepared iptables-restore scripts and tc/ip batch commands (for add and delete)

## v1.5.3

- Correctly reference named network namespace in ip netns exec calls
- Update dependencies

## v1.5.2

 - Update dependencies

## v1.5.1

 - Add STEADYBIT_EXTENSTION_DIG_TIMEOUT
 - Treat dns answers case insensitive

## v1.5.0

 - Run steadybit sidecar containers using crun
 - Support crun on openshift >= 4.18
 - Use stressng --iomix (instead of --io) to stress io

## v1.4.12

- If stress/diskfill/memfill exits unexpetedly report this as error and not as failure

## v1.4.11

- Improve perfomance when starting resource/network attacks

## v1.4.10

- safe defaults for stress-attacks
- action name inconsistencies
- update depdendencies

## v1.4.8

- add more prefill-queries
- remove dependency to lsns
- update depdendencies
- require iproute-tc and libcap instead of /usr/sbin/tc and /usr/sbin/capsh

## v1.4.7

- Update dependencies
- fix: fill disk fails when file permissions disallow write
- fix: stress io fails when file permissions disallow write

## v1.4.6

- Correctly handle missing network namespaces during stop action
- Update dependencies

## v1.4.5

- Stress cpu attack uses all configured CPUs not all CPUs available for the target container

## v1.4.4

- Update dependencies (CVE-2024-11187 & CVE-2024-12705)

## v1.4.3

- Rename some network actions to explicitly contain the term "outgoing"
- Use runc binary from the opencontainers/runc project

## v1.4.2

- Improve container id to be unique by adding the execution id

## v1.4.1

- Respect the container memory limit for stress-ng based actions
- Add option to disallow containers in certain namespaces
- Update dependencies

## v1.4.0

- Remove host network usage
- Drop all unneeded capabilities
- Make CAP_SYS_RESOURCE optional

## v1.3.30

- Use uid instead of name for user statement in Dockerfile

## v1.3.29

- Update dependencies

## v1.3.28

- fix: Network attack cannot be executed, after a previous attack skipped cleanup for missing container
- chore: update dependencies

## v1.3.27

- chore: use new signal handle mechanism from extension-kit
- chore: update dependencies

## v1.3.26

- chore: update dependencies
- fix: network actions if runc debug is enabled

## v1.3.25

- chore: update dependencies

## v1.3.24

- fix: only create network excludes which are necessary for the given includes
- fix: aggregate excludes to ip ranges if there are too many
- fix: fail early when too many tc rules are generated for a network attack

## v1.3.23

- chore: update action_kit_sdk dependency

## v1.3.22

- feat: change default value for "jitter" in "Network Delay" attack to false
- feat: add memfill attack

## v1.3.21

- fixed ip rule v6 support check
- chore: update dependencies

## v1.3.20

- chore: update dependencies

## v1.3.19

- fix: Don't use the priomap defaults for network attacks, this might lead to unexpected behavior when TOS is set in packets

## v1.3.18

- fix: Race condition in network attacks reporting attack for namespace still active, when it isn't

## v1.3.17

- feat: remove the restriction on cgroup2 mounts using nsdelegate

## v1.3.16

- fixed fallback attributes of AWS availability zones to not include Azure region

## v1.3.15

- fail actions early when cgroup2 nsdelegation is causing problems
- support cidrs filters for the network attacks

## v1.3.14

- Update dependencies (go 1.22)
- Added noop mode for diskfill attack to avoid errors when the disk is already full enough

## v1.3.13

- Update dependencies

## v1.3.12

- Update dependencies

## v1.3.11

- Update dependencies
- feat: add `host.domainname` attribute containing the host FQDN

## v1.3.10

- Update dependencies

## v1.3.9

- Update dependencies
- Pause container: action will stop if container is restarted

## v1.3.8

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
