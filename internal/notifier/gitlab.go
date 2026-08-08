package notifier

import (
	"context"
)

type Notifier interface {
	PostStatus(ctx context.Context, mrID int, status, url string) error
	DeleteStatus(ctx context.Context, mrID int) error
}

type GitLabNotifier struct {
	Token string
}

func (g *GitLabNotifier) PostStatus(ctx context.Context, mrID int, status, url string) error {
	// Post or update MR comment
	return nil
}

func (g *GitLabNotifier) DeleteStatus(ctx context.Context, mrID int) error {
	// Delete MR comment
	return nil
}
