package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/dynamic"
)

// ErrUninstall is an uninstallation that would strand a WorkerPool or did
// not see its pools finish.
var ErrUninstall = errors.New("worker controller uninstallation")

// UninstallInterval is how often Uninstall rereads the pools it waits for.
const UninstallInterval = 2 * time.Second

// Uninstall removes an installation's WorkerPools while its controller
// still runs. It refuses, deleting nothing, while a watch namespace holds a
// WorkerPool that selector does not match: once the controller is gone no
// replica would finalize it. Otherwise it deletes every pool selector
// matches and waits until each is gone, which the controller allows only
// after the pool's runs end, its workers deregister and its remote pool is
// deleted or released. When ctx ends first it names each pool that
// remains and the reason its Terminating condition gives.
func Uninstall(ctx context.Context, api dynamic.Interface, namespaces []string, selector string, interval time.Duration, log *slog.Logger) error {
	match, err := labels.Parse(selector)
	if err != nil || match.Empty() {
		return fmt.Errorf("%w: the selector %q names no pools", ErrUninstall, selector)
	}
	owned, err := uninstallScope(ctx, api, namespaces, match)
	if err != nil {
		return err
	}
	for _, wp := range owned {
		uid := wp.UID
		err := api.Resource(PoolResource).Namespace(wp.Namespace).Delete(ctx, wp.Name, metav1.DeleteOptions{
			Preconditions: &metav1.Preconditions{UID: &uid},
		})
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("%w: delete worker pool %s/%s: %w", ErrUninstall, wp.Namespace, wp.Name, err)
		}
		log.InfoContext(ctx, "deleting worker pool", "namespace", wp.Namespace, "name", wp.Name)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		remaining, err := uninstallScope(ctx, api, namespaces, match)
		if err != nil {
			return err
		}
		if len(remaining) == 0 {
			log.InfoContext(ctx, "every worker pool of the installation is gone")
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: worker pools remain: %s", ErrUninstall, terminating(remaining))
		case <-ticker.C:
		}
	}
}

// uninstallScope is every WorkerPool of namespaces, all of which match
// selector, or an error naming those that do not.
func uninstallScope(ctx context.Context, api dynamic.Interface, namespaces []string, selector labels.Selector) ([]WorkerPool, error) {
	var owned []WorkerPool
	var foreign []string
	for _, namespace := range namespaces {
		list, err := api.Resource(PoolResource).Namespace(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("%w: list worker pools in %s: %w", ErrUninstall, namespace, err)
		}
		for i := range list.Items {
			wp, err := decode[WorkerPool](&list.Items[i])
			if err != nil {
				return nil, err
			}
			if !selector.Matches(labels.Set(wp.Labels)) {
				foreign = append(foreign, wp.Namespace+"/"+wp.Name)
				continue
			}
			owned = append(owned, wp)
		}
	}
	if len(foreign) > 0 {
		slices.Sort(foreign)
		return nil, fmt.Errorf("%w: delete the worker pools this installation does not own first, since no controller would finalize them: %s",
			ErrUninstall, strings.Join(foreign, ", "))
	}
	return owned, nil
}

// terminating names each pool and, when its status gives one, the reason
// and message of its Terminating condition.
func terminating(pools []WorkerPool) string {
	out := make([]string, 0, len(pools))
	for _, wp := range pools {
		s := wp.Namespace + "/" + wp.Name
		if c := meta.FindStatusCondition(wp.Status.Conditions, ConditionTerminating); c != nil {
			s += " (" + c.Reason + ": " + c.Message + ")"
		}
		out = append(out, s)
	}
	return strings.Join(out, "; ")
}
