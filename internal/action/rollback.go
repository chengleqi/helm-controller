/*
Copyright 2022 The Flux authors

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

package action

import (
	helmaction "helm.sh/helm/v3/pkg/action"

	helmv2 "github.com/fluxcd/helm-controller/api/v2beta2"
)

// RollbackOption can be used to modify Helm's action.Rollback after the
// instructions from the v2beta2.HelmRelease have been applied. This is for
// example useful to enable the dry-run setting as a CLI.
type RollbackOption func(*helmaction.Rollback)

// Rollback runs the Helm rollback action with the provided config, using the
// v2beta2.HelmReleaseSpec of the given object to determine the target release
// and rollback configuration.
//
// It does not determine if there is a desire to perform the action, this is
// expected to be done by the caller. In addition, it does not take note of the
// action result. The caller is expected to listen on this using a
// storage.ObserveFunc, which provides superior access to Helm storage writes.
func Rollback(config *helmaction.Configuration, obj *helmv2.HelmRelease, opts ...RollbackOption) error {
	rollback := newRollback(config, obj, opts)
	return rollback.Run(obj.GetReleaseName())
}

func newRollback(config *helmaction.Configuration, obj *helmv2.HelmRelease, opts []RollbackOption) *helmaction.Rollback {
	rollback := helmaction.NewRollback(config)

	rollback.Timeout = obj.Spec.GetRollback().GetTimeout(obj.GetTimeout()).Duration
	rollback.Wait = !obj.Spec.GetRollback().DisableWait
	rollback.WaitForJobs = !obj.Spec.GetRollback().DisableWaitForJobs
	rollback.DisableHooks = obj.Spec.GetRollback().DisableHooks
	rollback.Force = obj.Spec.GetRollback().Force
	rollback.Recreate = obj.Spec.GetRollback().Recreate
	rollback.CleanupOnFail = obj.Spec.GetRollback().CleanupOnFail

	if prev := obj.Status.Previous; prev != nil && prev.Name == obj.GetReleaseName() && prev.Namespace == obj.GetReleaseNamespace() {
		rollback.Version = prev.Version
	}

	for _, opt := range opts {
		opt(rollback)
	}

	return rollback
}
