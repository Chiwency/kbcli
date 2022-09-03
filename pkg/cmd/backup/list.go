/*
Copyright © 2022 The OpenCli Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package backup

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/gosuri/uitable"
	"github.com/spf13/cobra"
	"helm.sh/helm/v3/pkg/cli/output"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/dynamic"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
	"k8s.io/kubectl/pkg/describe"

	"github.com/apecloud/kubeblocks/pkg/types"
	"github.com/apecloud/kubeblocks/pkg/utils"
)

type ListOptions struct {
	Namespace string

	Describer  func(*meta.RESTMapping) (describe.ResourceDescriber, error)
	NewBuilder func() *resource.Builder

	BuilderArgs []string

	EnforceNamespace bool
	AllNamespaces    bool

	DescriberSettings *describe.DescriberSettings
	FilenameOptions   *resource.FilenameOptions

	client dynamic.Interface
	genericclioptions.IOStreams
}

func NewListCmd(f cmdutil.Factory, streams genericclioptions.IOStreams) *cobra.Command {
	o := &ListOptions{
		FilenameOptions: &resource.FilenameOptions{},
		DescriberSettings: &describe.DescriberSettings{
			ShowEvents: true,
		},

		IOStreams: streams,
	}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all database backup job.",
		Run: func(cmd *cobra.Command, args []string) {
			cmdutil.CheckErr(o.Complete(f, args))
			cmdutil.CheckErr(o.Run())
		},
	}

	return cmd
}

func (o *ListOptions) Complete(f cmdutil.Factory, args []string) error {
	var err error
	o.Namespace, o.EnforceNamespace, err = f.ToRawKubeConfigLoader().Namespace()
	if err != nil {
		return err
	}

	if o.AllNamespaces {
		o.EnforceNamespace = false
	}

	o.BuilderArgs = append([]string{types.BackupJobSourceName}, args...)

	o.Describer = func(mapping *meta.RESTMapping) (describe.ResourceDescriber, error) {
		return describe.DescriberFn(f, mapping)
	}

	// used to fetch the resource
	config, err := f.ToRESTConfig()
	if err != nil {
		return nil
	}

	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return err
	}

	o.client = client
	o.NewBuilder = f.NewBuilder

	return nil
}

func (o *ListOptions) Run() error {
	r := o.NewBuilder().
		Unstructured().
		ContinueOnError().
		NamespaceParam(o.Namespace).DefaultNamespace().AllNamespaces(o.AllNamespaces).
		FilenameParam(o.EnforceNamespace, o.FilenameOptions).
		ResourceTypeOrNameArgs(true, o.BuilderArgs...).
		RequestChunksOf(o.DescriberSettings.ChunkSize).
		Flatten().
		Do()
	err := r.Err()
	if err != nil {
		return err
	}

	var allErrs []error
	infos, err := r.Infos()
	if err != nil {
		return err
	}

	table := uitable.New()
	table.AddRow("NAMESPACE", "NAME", "PHASE", "COMPLETION_TIME", "CREATE_TIME")
	errs := sets.NewString()
	for _, info := range infos {
		backupJobInfo := utils.BackupJobInfo{}

		mapping := info.ResourceMapping()
		if err != nil {
			if errs.Has(err.Error()) {
				continue
			}
			allErrs = append(allErrs, err)
			errs.Insert(err.Error())
			continue
		}

		backupJobInfo.Namespace = info.Namespace
		backupJobInfo.Name = info.Name
		obj, err := o.client.Resource(mapping.Resource).Namespace(o.Namespace).Get(context.TODO(), info.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		buildBackupJobInfo(obj, &backupJobInfo)
		table.AddRow(backupJobInfo.Namespace, backupJobInfo.Name, backupJobInfo.Phase, backupJobInfo.CompletionTime,
			backupJobInfo.StartTime)
	}

	_ = output.EncodeTable(o.Out, table)
	if len(infos) == 0 && len(allErrs) == 0 {
		// if we wrote no output, and had no errors, be sure we output something.
		if o.AllNamespaces {
			_, _ = fmt.Fprintln(o.ErrOut, "No resources found")
		} else {
			_, _ = fmt.Fprintf(o.ErrOut, "No resources found in %s namespace.\n", o.Namespace)
		}
	}
	return utilerrors.NewAggregate(allErrs)
}

func buildBackupJobInfo(obj *unstructured.Unstructured, info *utils.BackupJobInfo) {
	for k, v := range obj.GetLabels() {
		info.Labels = info.Labels + fmt.Sprintf("%s:%s ", k, v)
	}
	if obj.Object["status"] == nil {
		return
	}
	status := obj.Object["status"].(map[string]interface{})

	info.Name = obj.GetName()
	info.Namespace = obj.GetNamespace()
	if status["phase"] != nil {
		info.Phase = status["phase"].(string)
	}
	if status["completionTimestamp"] != nil {
		info.CompletionTime = status["completionTimestamp"].(string)
	}
	if status["startTimestamp"] != nil {
		info.StartTime = status["startTimestamp"].(string)
	}

}
