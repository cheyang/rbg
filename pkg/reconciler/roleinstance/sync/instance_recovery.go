/*
Copyright 2026 The RBG Authors.

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

package sync

import (
	"fmt"
	"sort"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// minStabilitySeconds is the floor of the stability window used to reset the
// consecutive-rebuild counter after the instance stays Ready.
const minStabilitySeconds = 60

// reconcileRestartPolicy runs the RecreateRoleInstanceOnPodRestart recovery state
// machine. It returns a non-nil expectationDiff when it wants to short-circuit normal
// reconciliation — to hold for the diagnosis window, to execute a whole-group rebuild,
// or to stop after the loop breaker trips — and nil to fall through to normal scaling.
//
// State machine (per reconcile):
//
//	not restarting:
//	  no crashers            -> maybe reset counter after sustained Ready; fall through
//	  crashers, preconds met -> enter hold (or trip loop breaker)
//	restarting, not yet rebuilt this cycle (HOLD):
//	  delay elapsed          -> execute whole-group rebuild
//	  otherwise              -> hold, requeue after remaining delay
//	restarting, rebuilt this cycle (POST-REBUILD WAIT):
//	  no crashers            -> fall through (recreate missing pods; Ready clears state)
//	  crashers again         -> repeat failure, open a new cycle (or trip loop breaker)
func (c *realControl) reconcileRestartPolicy(instance *workloadsv1alpha2.RoleInstance, allPods []*v1.Pod,
	baselines map[string]map[string]workloadsv1alpha2.ContainerUpdateBaseline) *expectationDiff {

	if instance.Spec.RestartPolicy != workloadsv1alpha2.RecreateRoleInstanceOnPodRestart {
		return nil
	}

	cfg := instance.Spec.RestartPolicyConfig
	delay := time.Duration(cfg.GetRebuildDelaySeconds()) * time.Second
	maxRebuilds := cfg.GetMaxConsecutiveRebuilds()

	// Terminal give-up state: do nothing until the operator intervenes (a spec change
	// bumps Generation) or the instance recovers (Ready clears the condition in the
	// status updater).
	if isBackoffExhausted(instance) {
		if instance.Generation != instance.Status.ObservedGeneration {
			clearBackoffExhausted(instance)
			instance.Status.ConsecutiveRestarts = 0
			// fall through: the new spec is handled by the normal update/scale paths.
			return nil
		}
		return &expectationDiff{}
	}

	restarting := isInstanceRestarting(instance)
	crashers := findCrashers(allPods, baselines)

	if !restarting {
		// Guard against informer lag: if we set the Restarting marker in a very recent
		// reconcile whose status write is not yet visible via the informer cache, the
		// in-memory cache still records it. Treat that as "already holding" and wait
		// rather than re-capturing the crash and refreshing the window.
		if _, held := restartingCache.Load(instanceKey(instance)); held {
			return &expectationDiff{requeueAfter: delay}
		}
		if len(crashers) == 0 {
			// Healthy: reset the loop counter once the instance has been Ready and stable.
			maybeResetConsecutiveRestarts(instance, delay)
			return nil
		}
		if !meetsRecreatePreconditions(instance, allPods) {
			return nil
		}
		return c.enterHoldOrBreak(instance, crashers, maxRebuilds, delay)
	}

	// restarting == true
	detTime := restartingSince(instance)
	if !rebuiltThisCycle(instance, detTime) {
		// HOLD phase — crashed pods are preserved for the diagnosis window.
		if detTime.IsZero() || time.Since(detTime) >= delay {
			return c.executeRebuild(instance, allPods)
		}
		return &expectationDiff{requeueAfter: delay - time.Since(detTime)}
	}

	// POST-REBUILD WAIT phase.
	if len(crashers) == 0 {
		// Fresh pods are starting/recovering; let normal scaling recreate any missing
		// pods. Once the instance is Ready again the recovery cycle is complete — clear
		// the Restarting marker (the state machine owns this condition).
		if wasInstanceReady(instance) {
			setCondition(instance, workloadsv1alpha2.RoleInstanceRestarting, v1.ConditionFalse,
				"Recovered", "instance recovered after whole-group rebuild")
			c.ClearRestarting(instance)
		}
		return nil
	}
	if !meetsRecreatePreconditions(instance, allPods) {
		return nil
	}
	// The rebuild did not fix the crash — open a new cycle (climbs toward the limit).
	return c.enterHoldOrBreak(instance, crashers, maxRebuilds, delay)
}

// enterHoldOrBreak either trips the loop breaker (when the consecutive-rebuild limit is
// reached) or records the crash, sets the Restarting marker, and holds for the diagnosis
// window before a rebuild.
func (c *realControl) enterHoldOrBreak(instance *workloadsv1alpha2.RoleInstance, crashers []*v1.Pod,
	maxRebuilds int32, delay time.Duration) *expectationDiff {

	if maxRebuilds > 0 && instance.Status.ConsecutiveRestarts >= maxRebuilds {
		setBackoffExhausted(instance)
		c.ClearRestarting(instance)
		c.recorder.Eventf(instance, v1.EventTypeWarning, "RestartBackoffExhausted",
			"restart policy gave up after %d consecutive rebuilds without a sustained-Ready instance; manual intervention required",
			instance.Status.ConsecutiveRestarts)
		klog.InfoS("restart-policy loop breaker tripped", "instance", klog.KObj(instance),
			"consecutiveRestarts", instance.Status.ConsecutiveRestarts, "limit", maxRebuilds)
		return &expectationDiff{}
	}

	instance.Status.LastCrashInfo = buildCrashInfo(crashers)
	c.recorder.Eventf(instance, v1.EventTypeWarning, "PodCrashDetected",
		"restart policy detected %d crashed pod(s); holding %s before whole-group rebuild for diagnosis (%s)",
		len(crashers), delay, describeCrashers(instance.Status.LastCrashInfo))
	setRestartingCondition(instance)
	return &expectationDiff{requeueAfter: delay}
}

// executeRebuild produces the whole-group delete diff and bumps the rebuild bookkeeping.
func (c *realControl) executeRebuild(instance *workloadsv1alpha2.RoleInstance, allPods []*v1.Pod) *expectationDiff {
	instance.Status.ConsecutiveRestarts++
	now := metav1.Now()
	instance.Status.LastRestartTime = &now
	c.recorder.Eventf(instance, v1.EventTypeNormal, "ReCreateInstance",
		"RestartPolicy is RecreateRoleInstanceOnPodRestart, recreate all pods of instance %s (rebuild #%d)",
		klog.KObj(instance), instance.Status.ConsecutiveRestarts)
	return &expectationDiff{toDeleteNum: len(allPods), toDeletePod: allPods}
}

// maybeResetConsecutiveRestarts clears the counter and the give-up condition once the
// instance has been Ready and stable for longer than the stability window. The window
// hysteresis prevents a crashloop that briefly flips Ready from perpetually resetting
// the counter and defeating the loop breaker.
func maybeResetConsecutiveRestarts(instance *workloadsv1alpha2.RoleInstance, delay time.Duration) {
	if instance.Status.ConsecutiveRestarts == 0 {
		return
	}
	if !wasInstanceReady(instance) {
		return
	}
	window := 2 * delay
	if window < minStabilitySeconds*time.Second {
		window = minStabilitySeconds * time.Second
	}
	// Require a known, sufficiently old last-restart time before trusting stability.
	if instance.Status.LastRestartTime == nil || time.Since(instance.Status.LastRestartTime.Time) < window {
		return
	}
	instance.Status.ConsecutiveRestarts = 0
	if isBackoffExhausted(instance) {
		clearBackoffExhausted(instance)
	}
}

// findCrashers returns the pods whose failure should trigger a whole-group rebuild,
// applying the same exclusions as the legacy shouldRecreateInstance: pods annotated with
// restart-trigger-policy=Ignore, pods mid in-place update, and container restarts that
// are accounted for by a recent in-place image change.
func findCrashers(pods []*v1.Pod,
	baselines map[string]map[string]workloadsv1alpha2.ContainerUpdateBaseline) []*v1.Pod {

	var crashers []*v1.Pod
	for _, p := range pods {
		if hasTriggerPolicyIgnore(p) {
			continue
		}
		if isPodInPlaceUpdating(p) {
			continue
		}
		if p.Status.Phase == v1.PodFailed && p.DeletionTimestamp == nil {
			crashers = append(crashers, p)
			continue
		}
		if containerRestarted(p) && !isContainerRestartExpected(p, baselines) {
			crashers = append(crashers, p)
		}
	}
	return crashers
}

// meetsRecreatePreconditions mirrors the stable-state gates of the legacy
// shouldRecreateInstance: pods exist, the instance was Ready, no update is in flight, and
// the revisions have converged.
func meetsRecreatePreconditions(instance *workloadsv1alpha2.RoleInstance, pods []*v1.Pod) bool {
	if len(pods) == 0 {
		return false
	}
	if !wasInstanceReady(instance) || instance.Generation != instance.Status.ObservedGeneration {
		return false
	}
	if instance.Status.CurrentRevision != instance.Status.UpdateRevision {
		return false
	}
	return true
}

// restartingSince returns the LastTransitionTime of the Restarting condition, used as the
// crash-detection anchor for the diagnosis window.
func restartingSince(instance *workloadsv1alpha2.RoleInstance) time.Time {
	for _, cond := range instance.Status.Conditions {
		if cond.Type == workloadsv1alpha2.RoleInstanceRestarting {
			return cond.LastTransitionTime.Time
		}
	}
	return time.Time{}
}

// rebuiltThisCycle reports whether a whole-group rebuild has already been executed for the
// current detection cycle (LastRestartTime is at or after the detection time).
func rebuiltThisCycle(instance *workloadsv1alpha2.RoleInstance, detTime time.Time) bool {
	lrt := instance.Status.LastRestartTime
	if lrt == nil {
		return false
	}
	return !lrt.Time.Before(detTime)
}

// isBackoffExhausted reports whether the loop breaker has tripped.
func isBackoffExhausted(instance *workloadsv1alpha2.RoleInstance) bool {
	for _, cond := range instance.Status.Conditions {
		if cond.Type == workloadsv1alpha2.RoleInstanceRestartBackoffExhausted {
			return cond.Status == v1.ConditionTrue
		}
	}
	return false
}

// setBackoffExhausted marks the loop breaker as tripped and takes the instance out of the
// restarting state.
func setBackoffExhausted(instance *workloadsv1alpha2.RoleInstance) {
	setCondition(instance, workloadsv1alpha2.RoleInstanceRestartBackoffExhausted, v1.ConditionTrue,
		"RestartBackoffExhausted",
		fmt.Sprintf("stopped rebuilding after %d consecutive rebuilds without a sustained-Ready instance", instance.Status.ConsecutiveRestarts))
	setCondition(instance, workloadsv1alpha2.RoleInstanceRestarting, v1.ConditionFalse,
		"RestartBackoffExhausted", "restart policy stopped rebuilding")
}

// clearBackoffExhausted sets the give-up condition to False.
func clearBackoffExhausted(instance *workloadsv1alpha2.RoleInstance) {
	setCondition(instance, workloadsv1alpha2.RoleInstanceRestartBackoffExhausted, v1.ConditionFalse,
		"Recovered", "restart policy resumed")
}

// setCondition sets (or updates) a condition on the instance status in place.
func setCondition(instance *workloadsv1alpha2.RoleInstance, t workloadsv1alpha2.RoleInstanceConditionType,
	status v1.ConditionStatus, reason, message string) {
	for i, cond := range instance.Status.Conditions {
		if cond.Type == t {
			if cond.Status != status {
				instance.Status.Conditions[i].LastTransitionTime = metav1.Now()
			}
			instance.Status.Conditions[i].Status = status
			instance.Status.Conditions[i].Reason = reason
			instance.Status.Conditions[i].Message = message
			return
		}
	}
	instance.Status.Conditions = append(instance.Status.Conditions, workloadsv1alpha2.RoleInstanceCondition{
		Type:               t,
		Status:             status,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	})
}

// buildCrashInfo captures diagnostic evidence for the crashed pods, sorted by termination
// time (earliest first), flagging the earliest as the likely root cause. Best-effort.
func buildCrashInfo(crashers []*v1.Pod) *workloadsv1alpha2.RoleInstanceCrashInfo {
	if len(crashers) == 0 {
		return nil
	}
	pods := make([]workloadsv1alpha2.CrashedPod, 0, len(crashers))
	for _, p := range crashers {
		pods = append(pods, crashedPodOf(p))
	}
	sort.SliceStable(pods, func(i, j int) bool {
		return finishedBefore(pods[i].FinishedAt, pods[j].FinishedAt)
	})
	if len(pods) > 0 {
		pods[0].LikelyRootCause = true
	}
	return &workloadsv1alpha2.RoleInstanceCrashInfo{
		ObservedTime: metav1.Now(),
		Pods:         pods,
	}
}

// crashedPodOf extracts the termination details of the most relevant container in a pod.
func crashedPodOf(p *v1.Pod) workloadsv1alpha2.CrashedPod {
	cp := workloadsv1alpha2.CrashedPod{
		PodName:  p.Name,
		NodeName: p.Spec.NodeName,
	}
	for _, cs := range p.Status.ContainerStatuses {
		term := terminatedState(cs)
		if term == nil {
			continue
		}
		// Prefer the container with the highest restart count / a real error.
		if cs.RestartCount >= cp.RestartCount {
			cp.Container = cs.Name
			cp.ExitCode = term.ExitCode
			cp.Reason = term.Reason
			cp.RestartCount = cs.RestartCount
			ft := term.FinishedAt
			cp.FinishedAt = &ft
		}
	}
	return cp
}

// terminatedState returns the terminated state of a container, preferring the current
// state and falling back to the last termination state (populated after a restart).
func terminatedState(cs v1.ContainerStatus) *v1.ContainerStateTerminated {
	if cs.State.Terminated != nil {
		return cs.State.Terminated
	}
	if cs.LastTerminationState.Terminated != nil {
		return cs.LastTerminationState.Terminated
	}
	return nil
}

func finishedBefore(a, b *metav1.Time) bool {
	if a == nil {
		return false
	}
	if b == nil {
		return true
	}
	return a.Time.Before(b.Time)
}

func describeCrashers(info *workloadsv1alpha2.RoleInstanceCrashInfo) string {
	if info == nil || len(info.Pods) == 0 {
		return ""
	}
	root := info.Pods[0]
	return fmt.Sprintf("likely root cause: pod=%s node=%s container=%s exitCode=%d reason=%s",
		root.PodName, root.NodeName, root.Container, root.ExitCode, root.Reason)
}
