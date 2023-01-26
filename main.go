package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	wfclient "github.com/argoproj/argo-workflows/v3/cmd/argo/commands/client"
	cwf "github.com/argoproj/argo-workflows/v3/pkg/apiclient/cronworkflow"
	wfv1alpha1 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
	"github.com/robfig/cron/v3"
	"github.com/spf13/pflag"
	"golang.org/x/exp/maps"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
)

const commandName = "kubectl-cls"

func main() {
	if err := run(os.Stdout, os.Stderr, os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(stdout, stderr io.Writer, args []string) error {
	// Parse flags
	// -----------------
	var (
		fromFlag       string
		toFlag         string
		noHeadersFlag  bool
		outputFlag     string
		selectorFlag   string
		showLabelsFlag bool
		versionFlag    bool
	)
	fsets := pflag.NewFlagSet(commandName, pflag.ContinueOnError)
	fsets.SetOutput(stderr)
	fsets.StringVarP(&fromFlag, "from", "", "", "The start time of the period. e.g. '2023-01-24T00:00:00+09:00'.")
	fsets.StringVarP(&toFlag, "to", "", "", "The end time of the period. e.g. '2023-01-24T00:00:00+09:00'.")
	fsets.BoolVarP(&noHeadersFlag, "no-headers", "", false, "If present, don't print headers.")
	fsets.StringVarP(&outputFlag, "output", "o", "", "Output format. One of: ''|json.")
	fsets.StringVarP(&selectorFlag, "selector", "l", "", "Selector (label query) to filter on, supports '=', '==', and '!='.(e.g. -l key1=value1,key2=value2).")
	fsets.BoolVarP(&showLabelsFlag, "show-labels", "", false, "When printing, show all labels as the last column (default hide labels column)")
	fsets.BoolVarP(&versionFlag, "version", "V", false, "Prints version information.")
	cfgFlags := genericclioptions.NewConfigFlags(true)
	cfgFlags.AddFlags(fsets)

	fsets.Usage = func() {
		fmt.Fprintf(stderr, "Usage of %s:\n", commandName)
		fmt.Fprint(stderr, fsets.FlagUsages())
	}

	if err := fsets.Parse(args[1:]); err != nil {
		return err
	}

	if versionFlag {
		fmt.Fprintf(stdout, "%s %s (rev:%s)\n", commandName, Version, Revision)
		return nil
	}

	var (
		err        error
		timeLayout = "2006-01-02T15:04:05Z07:00"
		from       time.Time
		to         time.Time
	)

	// Set the start time of the period.
	// -----------------
	if fromFlag == "" {
		return errors.New("please set --from flag")
	}
	from, err = time.Parse(timeLayout, fromFlag)
	if err != nil {
		return fmt.Errorf("failed to parse '--from' value: %w", err)
	}
	from = from.UTC() // Convert to UTC for easy comparison with the schedule.

	// Set the end time of the period.
	// -----------------
	if toFlag == "" {
		return errors.New("please set --to flag")
	}
	to, err = time.Parse(timeLayout, toFlag)
	if err != nil {
		return fmt.Errorf("failed to parse '--to' value: %w", err)
	}
	to = from.UTC() // Convert to UTC for easy comparison with the schedule.

	// Validation
	// -----------------
	if from.After(to) {
		return errors.New("'--from' '--to' times are reversed")
	}
	if outputFlag != "" && outputFlag != "json" {
		return fmt.Errorf("%s is unsupported output format", outputFlag)
	}

	// List CronJobs
	// -----------------
	cfg, err := cfgFlags.ToRESTConfig()
	if err != nil {
		return fmt.Errorf("failed to get kubernetes REST client configuration: %w", err)
	}

	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("failed to get kubernetes client: %w", err)
	}

	targetNamespace := ""
	if *cfgFlags.Namespace != "" {
		targetNamespace = *cfgFlags.Namespace
	}

	cronjobList, err := client.BatchV1().CronJobs(targetNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: selectorFlag})
	if err != nil {
		if targetNamespace == "" {
			targetNamespace = "all"
		}
		return fmt.Errorf("failed to get CronJobs in '%s' namespace: %w", targetNamespace, err)
	}
	includedCronJobs, err := getScheduleIncludedCronJobs(cronjobList.Items, from, to)
	if err != nil {
		return fmt.Errorf("failed to get CronJobs in the from-to period: %w", err)
	}

	// List CronWorkflows
	// -----------------
	ctx, wfAPIClient := wfclient.NewAPIClient(context.Background())
	cwfClient, _ := wfAPIClient.NewCronWorkflowServiceClient()
	cronworkflowList, err := cwfClient.ListCronWorkflows(ctx, &cwf.ListCronWorkflowsRequest{Namespace: targetNamespace, ListOptions: &metav1.ListOptions{LabelSelector: selectorFlag}}, nil)
	if err != nil {
		if targetNamespace == "" {
			targetNamespace = "all"
		}
		return fmt.Errorf("failed to get CronWorkflow in '%s' namespace: %w", targetNamespace, err)
	}
	includedCronWorkflows, err := getScheduleIncludedCronWorkflows(cronworkflowList.Items, from, to)
	if err != nil {
		return fmt.Errorf("failed to get CronWorkflows in the from-to period: %w", err)
	}

	// PrintResults
	// -----------------
	switch outputFlag {
	case "json":
		printJSON(stdout, includedCronJobs, includedCronWorkflows)
	case "":
		printList(stdout, noHeadersFlag, showLabelsFlag, includedCronJobs, includedCronWorkflows)
	}

	return nil
}

// Extract CronJobs to be executed during the from-to period.
func getScheduleIncludedCronJobs(cronjobs []batchv1.CronJob, from, to time.Time) ([]batchv1.CronJob, error) {
	// If there is no CronJob in the specified Namespace, return early.
	if len(cronjobs) == 0 {
		return []batchv1.CronJob{}, nil
	}

	// Extract CronJobs to be executed during the from-to period.
	scheduleIncludedCronJob := map[string]batchv1.CronJob{}
	for _, cronjob := range cronjobs {
		sched, err := cron.ParseStandard(cronjob.Spec.Schedule)
		if err != nil {
			return nil, fmt.Errorf("failed to parse schedule spec '%s' of CronJob '%s/%s': %w", cronjob.Spec.Schedule, cronjob.Namespace, cronjob.Name, err)
		}
		if isInclude(sched, from, to) {
			scheduleIncludedCronJob[cronjob.Namespace+cronjob.Name] = cronjob
		}
	}

	// sort
	sortedKeys := maps.Keys(scheduleIncludedCronJob)
	sort.Strings(sortedKeys)
	ret := make([]batchv1.CronJob, len(sortedKeys))
	for i, sortedKey := range sortedKeys {
		ret[i] = scheduleIncludedCronJob[sortedKey]
	}

	return ret, nil
}

// Extract CronWorkflows list to be executed during the from-to period.
func getScheduleIncludedCronWorkflows(cronworkflows []wfv1alpha1.CronWorkflow, from, to time.Time) ([]wfv1alpha1.CronWorkflow, error) {
	// If there is no CronJob in the specified Namespace, return early.
	if len(cronworkflows) == 0 {
		return []wfv1alpha1.CronWorkflow{}, nil
	}

	// Extract CronWorkflows to be executed during the from-to period.
	scheduleIncludedCronWorkflow := map[string]wfv1alpha1.CronWorkflow{}
	for _, cronworkflow := range cronworkflows {
		sched, err := cron.ParseStandard(cronworkflow.Spec.Schedule)
		if err != nil {
			return nil, fmt.Errorf("failed to parse schedule spec '%s' of CronJob '%s/%s': %w", cronworkflow.Spec.Schedule, cronworkflow.Namespace, cronworkflow.Name, err)
		}
		if isInclude(sched, from, to) {
			scheduleIncludedCronWorkflow[cronworkflow.Namespace+cronworkflow.Name] = cronworkflow
		}
	}

	// sort
	sortedKeys := maps.Keys(scheduleIncludedCronWorkflow)
	sort.Strings(sortedKeys)
	ret := make([]wfv1alpha1.CronWorkflow, len(sortedKeys))
	for i, sortedKey := range sortedKeys {
		ret[i] = scheduleIncludedCronWorkflow[sortedKey]
	}

	return ret, nil
}

// Whether the schedule is included in the from-to period.
func isInclude(sched cron.Schedule, from, to time.Time) bool {
	// To include the 'from' time in the from-to period.
	from = from.Add(-1 * time.Second)

	next := sched.Next(from)

	// To include the 'to' time in the from-to period.
	if next.Equal(to) {
		return true
	}

	if next.After(to) {
		return false
	}

	return true
}

func printList(stdout io.Writer, noHeaders, showLabels bool, cronjobs []batchv1.CronJob, cronworkflows []wfv1alpha1.CronWorkflow) {
	tw := tabwriter.NewWriter(stdout, 0, 1, 3, ' ', 0)
	if !noHeaders {
		if showLabels {
			fmt.Fprintln(tw, "Namespace\tName\tSchedule\tSuspend\tKind\tLabels")
		} else {
			fmt.Fprintln(tw, "Namespace\tName\tSchedule\tSuspend\tKind")
		}
	}

	if len(cronjobs) != 0 {
		for _, cronjob := range cronjobs {
			if showLabels {
				labels := make([]string, len(cronjob.GetLabels()))
				i := 0
				for k, v := range cronjob.GetLabels() {
					labels[i] = fmt.Sprintf("%s=%s", k, v)
					i++
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%t\tCronJob\t%s\n", cronjob.Namespace, cronjob.Name, cronjob.Spec.Schedule, *cronjob.Spec.Suspend, strings.Join(labels, ","))
			} else {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%t\tCronJob\n", cronjob.Namespace, cronjob.Name, cronjob.Spec.Schedule, *cronjob.Spec.Suspend)
			}
		}
	}

	if len(cronworkflows) != 0 {
		for _, cronworkflow := range cronworkflows {
			if showLabels {
				labels := make([]string, len(cronworkflow.GetLabels()))
				i := 0
				for k, v := range cronworkflow.GetLabels() {
					labels[i] = fmt.Sprintf("%s=%s", k, v)
					i++
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%t\tCronWorkflow\t%s\n", cronworkflow.Namespace, cronworkflow.Name, cronworkflow.Spec.Schedule, cronworkflow.Spec.Suspend, strings.Join(labels, ","))
			} else {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%t\tCronWorkflow\n", cronworkflow.Namespace, cronworkflow.Name, cronworkflow.Spec.Schedule, cronworkflow.Spec.Suspend)
			}
		}
	}

	tw.Flush()
}

type printformat struct {
	ApiVersion string `json:"apiVersion"`
	Items      []any  `json:"items"`
}

func buildPrintformat(cronjobs []batchv1.CronJob, cronworkflows []wfv1alpha1.CronWorkflow) printformat {
	items := make([]any, len(cronjobs)+len(cronworkflows))

	for i, item := range cronjobs {
		// manualy set TypeMeta manually because of this bug:
		// https://github.com/kubernetes/client-go/issues/308
		item.TypeMeta.APIVersion = "v1"
		item.TypeMeta.Kind = "CronJob"
		items[i] = item
	}
	for i, item := range cronworkflows {
		// manualy set TypeMeta manually because of this bug:
		// https://github.com/kubernetes/client-go/issues/308
		item.TypeMeta.APIVersion = "argoproj.io/v1alpha1"
		item.TypeMeta.Kind = "CronWorkflow"
		items[i+len(cronjobs)] = item
	}

	return printformat{
		ApiVersion: "v1",
		Items:      items,
	}
}

func printJSON(stdout io.Writer, cronjobs []batchv1.CronJob, cronworkflows []wfv1alpha1.CronWorkflow) error {
	pf := buildPrintformat(cronjobs, cronworkflows)
	b, err := json.MarshalIndent(pf, "", "    ")
	if err != nil {
		return fmt.Errorf("failed to marshal to json: %w", err)
	}
	fmt.Fprint(stdout, string(b))
	return nil
}
