package webhook

import "context"

// CommentPoster defines the interface for posting comments back to SCM pull/merge requests.
type CommentPoster interface {
	PostComment(ctx context.Context, project string, mrID int, body string) (int64, error)
}
