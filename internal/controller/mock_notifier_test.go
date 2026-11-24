package controller

import (
	"context"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

type mockNotifier struct {
	nextCommentID   int64
	failNextCreated error
	failNextReady   error
	failNextFailed  error
	failNextStatus  error
	createdCalls    []*divergeiov1alpha1.PreviewGroup
	readyCalls      []*divergeiov1alpha1.PreviewGroup
	failedCalls     []struct {
		pg     *divergeiov1alpha1.PreviewGroup
		reason string
	}
	teardownCalls []*divergeiov1alpha1.PreviewGroup
	statusCalls   []*divergeiov1alpha1.PreviewGroup
}

func (m *mockNotifier) assignCommentID(pg *divergeiov1alpha1.PreviewGroup) {
	if m.nextCommentID > 0 && pg.Status.CommentID == 0 {
		pg.Status.CommentID = m.nextCommentID
		m.nextCommentID++
	}
}

func (m *mockNotifier) PostGroupCreated(ctx context.Context, pg *divergeiov1alpha1.PreviewGroup) error {
	m.createdCalls = append(m.createdCalls, pg)
	if m.failNextCreated != nil {
		err := m.failNextCreated
		m.failNextCreated = nil
		return err
	}
	m.assignCommentID(pg)
	return nil
}

func (m *mockNotifier) PostGroupReady(ctx context.Context, pg *divergeiov1alpha1.PreviewGroup) error {
	m.readyCalls = append(m.readyCalls, pg)
	if m.failNextReady != nil {
		err := m.failNextReady
		m.failNextReady = nil
		return err
	}
	m.assignCommentID(pg)
	return nil
}

func (m *mockNotifier) PostGroupFailed(ctx context.Context, pg *divergeiov1alpha1.PreviewGroup, reason string) error {
	m.failedCalls = append(m.failedCalls, struct {
		pg     *divergeiov1alpha1.PreviewGroup
		reason string
	}{pg, reason})
	if m.failNextFailed != nil {
		err := m.failNextFailed
		m.failNextFailed = nil
		return err
	}
	m.assignCommentID(pg)
	return nil
}

func (m *mockNotifier) PostGroupTeardown(ctx context.Context, pg *divergeiov1alpha1.PreviewGroup) error {
	m.teardownCalls = append(m.teardownCalls, pg)
	return nil
}

func (m *mockNotifier) UpdateGroupStatus(ctx context.Context, pg *divergeiov1alpha1.PreviewGroup) error {
	m.statusCalls = append(m.statusCalls, pg)
	if m.failNextStatus != nil {
		err := m.failNextStatus
		m.failNextStatus = nil
		return err
	}
	return nil
}
