package main

import (
	"fmt"
	"testing"
	"time"

	wfv1alpha1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	"github.com/google/go-cmp/cmp"
	"github.com/robfig/cron/v3"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func getTime(value string) time.Time {
	t, err := time.Parse("2006-01-02T15:04:05Z07:00", value)
	if err != nil {
		msg := fmt.Sprintf("failed to get time from value '%s': %s", value, err)
		panic(msg)
	}
	return t.UTC() // See comment in fromFlag and toFlag
}

func getSchedule(spec string) cron.Schedule {
	sched, err := cron.ParseStandard(spec)
	if err != nil {
		msg := fmt.Sprintf("failed to get schedule from spec '%s': %s", spec, err)
		panic(msg)
	}
	return sched
}

func getCronJob(namespace, name, schedule string, suspend bool) batchv1.CronJob {
	return batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: batchv1.CronJobSpec{
			Schedule: schedule,
			Suspend:  &suspend,
		},
	}
}

func getCronWorkflow(namespace, name, schedule string, suspend bool) wfv1alpha1.CronWorkflow {
	return wfv1alpha1.CronWorkflow{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: wfv1alpha1.CronWorkflowSpec{
			Schedule: schedule,
			Suspend:  suspend,
		},
	}
}

func Test_isInclude(t *testing.T) {
	t.Parallel()
	type args struct {
		sched cron.Schedule
		from  time.Time
		to    time.Time
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "include test",
			args: args{
				sched: getSchedule("* * * * *"),
				from:  getTime("2023-01-24T00:00:00Z"),
				to:    getTime("2023-01-24T01:00:00Z"),
			},
			want: true,
		},
		{
			name: "exclude test",
			args: args{
				sched: getSchedule("0 3 * * *"),
				from:  getTime("2023-01-24T00:00:00Z"),
				to:    getTime("2023-01-24T01:00:00Z"),
			},
			want: false,
		},
		{
			name: "boundary test 1",
			args: args{
				sched: getSchedule("0 0 * * *"),
				from:  getTime("2023-01-24T00:00:00Z"),
				to:    getTime("2023-01-24T01:00:00Z"),
			},
			want: true,
		},
		{
			name: "boundary test 2",
			args: args{
				sched: getSchedule("0 1 * * *"),
				from:  getTime("2023-01-24T00:00:00Z"),
				to:    getTime("2023-01-24T01:00:00Z"),
			},
			want: true,
		},
		{
			name: "step spec test",
			args: args{
				sched: getSchedule("*/10 * * * *"),
				from:  getTime("2023-01-24T00:00:00Z"),
				to:    getTime("2023-01-24T01:00:00Z"),
			},
			want: true,
		},
		{
			name: "list spec test",
			args: args{
				sched: getSchedule("0,30 0,1,2 * * *"),
				from:  getTime("2023-01-24T00:00:00Z"),
				to:    getTime("2023-01-24T01:00:00Z"),
			},
			want: true,
		},
		{
			name: "range spec test",
			args: args{
				sched: getSchedule("0-30 0-2 * * *"),
				from:  getTime("2023-01-24T00:00:00Z"),
				to:    getTime("2023-01-24T01:00:00Z"),
			},
			want: true,
		},
		{
			name: "range and step spec test",
			args: args{
				sched: getSchedule("0-30/5 0-2/1 * * *"),
				from:  getTime("2023-01-24T00:00:00Z"),
				to:    getTime("2023-01-24T01:00:00Z"),
			},
			want: true,
		},
		{
			name: "location test",
			args: args{
				sched: getSchedule("30 2 * * *"),            // UTC
				from:  getTime("2023-01-24T11:00:00+09:00"), // JST
				to:    getTime("2023-01-24T12:00:00+09:00"), // JST
			},
			want: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isInclude(tt.args.sched, tt.args.from, tt.args.to); got != tt.want {
				t.Errorf("isInclude() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_getScheduleIncludedCronJobs(t *testing.T) {
	t.Parallel()
	type args struct {
		cronjobs []batchv1.CronJob
		from     time.Time
		to       time.Time
	}
	tests := []struct {
		name    string
		args    args
		want    []batchv1.CronJob
		wantErr bool
	}{
		{
			name: "basic test",
			args: args{
				cronjobs: []batchv1.CronJob{
					getCronJob("ns-b", "n-3", "0 1 * * *", false),
					getCronJob("ns-a", "n-1", "*/5 0 * * *", false),
					getCronJob("ns-b", "n-2", "0 2 * * *", false),
					getCronJob("ns-a", "n-4", "0-30 * * * *", false),
				},
				from: getTime("2023-01-24T00:00:00Z"),
				to:   getTime("2023-01-24T01:00:00Z"),
			},
			want: []batchv1.CronJob{
				getCronJob("ns-a", "n-1", "*/5 0 * * *", false),
				getCronJob("ns-a", "n-4", "0-30 * * * *", false),
				getCronJob("ns-b", "n-3", "0 1 * * *", false),
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := getScheduleIncludedCronJobs(tt.args.cronjobs, tt.args.from, tt.args.to)
			if (err != nil) != tt.wantErr {
				t.Errorf("getScheduleIncludedCronJobs() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("getScheduleIncludedCronJobs() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func Test_getScheduleIncludedCronWorkflows(t *testing.T) {
	t.Parallel()
	type args struct {
		cronworkflows []wfv1alpha1.CronWorkflow
		from          time.Time
		to            time.Time
	}
	tests := []struct {
		name    string
		args    args
		want    []wfv1alpha1.CronWorkflow
		wantErr bool
	}{
		{
			name: "basic test",
			args: args{
				cronworkflows: []wfv1alpha1.CronWorkflow{
					getCronWorkflow("ns-b", "n-3", "0 1 * * *", false),
					getCronWorkflow("ns-a", "n-1", "*/5 0 * * *", false),
					getCronWorkflow("ns-b", "n-2", "0 2 * * *", false),
					getCronWorkflow("ns-a", "n-4", "0-30 * * * *", false),
				},
				from: getTime("2023-01-24T00:00:00Z"),
				to:   getTime("2023-01-24T01:00:00Z"),
			},
			want: []wfv1alpha1.CronWorkflow{
				getCronWorkflow("ns-a", "n-1", "*/5 0 * * *", false),
				getCronWorkflow("ns-a", "n-4", "0-30 * * * *", false),
				getCronWorkflow("ns-b", "n-3", "0 1 * * *", false),
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := getScheduleIncludedCronWorkflows(tt.args.cronworkflows, tt.args.from, tt.args.to)
			if (err != nil) != tt.wantErr {
				t.Errorf("getScheduleIncludedCronWorkflows() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("getScheduleIncludedCronWorkflows() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
