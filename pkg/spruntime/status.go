package spruntime

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TODO: Move status fuctions and constants to separate repository
const (
	// ServiceProviderConditionReady is the condition type used when reporting status
	ServiceProviderConditionReady = "Ready"
	// StatusPhaseReady indicates that the resource is ready. All conditions are met and are in status "True".
	StatusPhaseReady = "Ready"
	// StatusPhaseProgressing indicates that the resource is not ready and being created or updated.
	StatusPhaseProgressing = "Progressing"
	// StatusPhaseFailed indicates that the resource is failing.
	StatusPhaseFailed = "Failed"
	// StatusPhaseTerminating indicates that the resource is not ready and in deletion.
	StatusPhaseTerminating = "Terminating"
)

// StatusProgressing indicates progressing with synced false
func StatusProgressing(obj ServiceProviderAPI, reason string, message string) {
	meta.SetStatusCondition(obj.GetConditions(), metav1.Condition{
		Type:               ServiceProviderConditionReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: obj.GetGeneration(),
		Reason:             reason,
		Message:            message,
	})
	obj.SetObservedGeneration(obj.GetGeneration())
	obj.SetPhase(StatusPhaseProgressing)
}

// StatusReady indicates ready with ready true
func StatusReady(obj ServiceProviderAPI) {
	meta.SetStatusCondition(obj.GetConditions(), metav1.Condition{
		Type:               ServiceProviderConditionReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: obj.GetGeneration(),
		Reason:             "ReconcileSuccess",
		Message:            "Domain Service is ready",
	})
	obj.SetObservedGeneration(obj.GetGeneration())
	obj.SetPhase(StatusPhaseReady)
}

// StatusTerminating indicates terminating with synced false
func StatusTerminating(obj ServiceProviderAPI) {
	meta.SetStatusCondition(obj.GetConditions(), metav1.Condition{
		Type:               ServiceProviderConditionReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: obj.GetGeneration(),
		Reason:             "Terminating",
		Message:            "Cleanup in progress",
	})
	obj.SetObservedGeneration(obj.GetGeneration())
	obj.SetPhase(StatusPhaseTerminating)
}
