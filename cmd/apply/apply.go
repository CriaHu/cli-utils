/*
Copyright 2019 The Kubernetes Authors.
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

package apply

import (
	"fmt"

	"github.com/spf13/cobra"
	"sigs.k8s.io/cli-utils/internal/pkg/clik8s"
	"sigs.k8s.io/cli-utils/internal/pkg/util"
	"sigs.k8s.io/cli-utils/internal/pkg/wirecli/wireapply"
)

// GetApplyCommand returns the `apply` cobra Command
func GetApplyCommand(a util.Args) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply resource configurations.",
		Long: `Apply resource configurations to k8s cluster. 
The resource configurations can be from a Kustomization directory.
The path of the resource configurations should be passed to apply
as an argument.

	# Apply the configurations from a directory containing kustomization.yaml - e.g. dir/kustomization.yaml
	k2 apply dir

When server-side apply is available on the cluster, it is used; otherwise, client-side apply
is used.
`,
		Args: cobra.MinimumNArgs(1),
	}

	cmd.Flags().Bool("prune", false, "Declarative delete.")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		for i := range args {
			apply, err := wireapply.InitializeApply(clik8s.ResourceConfigPath(args[i]), cmd.OutOrStdout(), a)
			if err != nil {
				return err
			}
			prune, perr := cmd.Flags().GetBool("prune")
			if perr != nil {
				return perr
			}
			apply.Prune = prune

			r, aerr := wireapply.NewApplyCommandResult(apply, cmd.OutOrStdout())
			if aerr != nil {
				return aerr
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Resources: %v\n", len(r.Resources))
		}
		return nil
	}

	return cmd
}
