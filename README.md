# kubectl-cls

List CronJobs and CronWorkflows that are scheduled and executed during a specific time period.

## Installation

### Homebrew

TBD

### Go

```
go install github.com/unblee/kubectl-cls@latest
```

### Binaries

TBD

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
git tag -a vX.Y.Z
git push origin vX.Y.Z
```
