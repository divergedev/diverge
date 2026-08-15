package controller

import (
	"context"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

type mockNotifier struct {
	createdCalls []*divergeiov1alpha1.PreviewGroup
	readyCalls   []*divergeiov1alpha1.PreviewGroup
	failedCalls  []struct {
		pg     *divergeiov1alpha1.PreviewGroup
		reason string
	}
	teardownCalls []*divergeiov1alpha1.PreviewGroup
	statusCalls   []*divergeiov1alpha1.PreviewGroup
}

func (m *mockNotifier) PostGroupCreated(ctx context.Context, pg *divergeiov1alpha1.PreviewGroup) error {
	m.createdCalls = append(m.createdCalls, pg)
	return nil
}

func (m *mockNotifier) PostGroupReady(ctx context.Context, pg *divergeiov1alpha1.PreviewGroup) error {
	m.readyCalls = append(m.readyCalls, pg)
	return nil
}

func (m *mockNotifier) PostGroupFailed(ctx context.Context, pg *divergeiov1alpha1.PreviewGroup, reason string) error {
	m.failedCalls = append(m.failedCalls, struct {
		pg     *divergeiov1alpha1.PreviewGroup
		reason string
	}{pg, reason})
	return nil
}

func (m *mockNotifier) PostGroupTeardown(ctx context.Context, pg *divergeiov1alpha1.PreviewGroup) error {
	m.teardownCalls = append(m.teardownCalls, pg)
	return nil
}

func (m *mockNotifier) UpdateGroupStatus(ctx context.Context, pg *divergeiov1alpha1.PreviewGroup) error {
	m.statusCalls = append(m.statusCalls, pg)
	return nil
}
