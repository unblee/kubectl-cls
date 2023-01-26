# kubectl-cls

List CronJobs and CronWorkflows that are scheduled and executed during a specific time period.

## Installation

### Homebrew

```
# tap and install
$ brew tap unblee/tap
$ brew install kubectl-cls

# install directly
$ brew install unblee/tap/kubectl-cls
```

### Go

```
go install github.com/unblee/kubectl-cls@latest
```

### Binaries

See [releases page](https://github.com/unblee/kubectl-cls/releases).

## Usage

The `--from` and `--to` options must be specified. Its format is RFC3339.

```
$ kubectl cls --from 2023-01-24T00:00:00+09:00 --to 2023-01-24T06:00:00+09:00
Namespace     Name   Schedule             Suspend   Kind
namespace-a   foo    */10 * * * *         false     CronJob
namespace-b   bar    0 15 * * *           false     CronJob
namespace-c   baz    0,15,30,45 * * * *   false     CronJob
namespace-z   qux    */30 * * * *         false     CronWorkflow
namespace-z   quux   0 * * * *            false     CronWorkflow
```

## Note

The Kubernetes cluster is assumed to be running in UTC.

## Release

```
git tag vX.Y.Z
git push origin vX.Y.Z
```
