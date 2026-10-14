package v1alpha1

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestValidateAgentTask(t *testing.T) {
	tests := []struct {
		name      string
		task      *AgentTask
		wantErrs  int
		wantField string
	}{
		{
			name: "valid minimal AgentTask",
			task: &AgentTask{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fix-bug-1",
					Namespace: "default",
				},
				Spec: AgentTaskSpec{
					Objective: "Fix NPE in payment handler",
					Repository: AgentTaskRepository{
						URL: "https://github.com/org/repo.git",
					},
				},
			},
			wantErrs: 0,
		},
		{
			name: "valid AgentTask with ssh/git@ URL and budgets",
			task: &AgentTask{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fix-bug-2",
					Namespace: "agents",
				},
				Spec: AgentTaskSpec{
					Objective: "Add rate limiting middleware",
					Repository: AgentTaskRepository{
						URL:           "git@github.com:org/repo.git",
						BaseBranch:    "main",
						WorkingBranch: "agent/rate-limit",
					},
					BudgetUSD:     "5.00",
					BudgetTokens:  500000,
					MaxIterations: 5,
					Sandbox: AgentTaskSandbox{
						Provider:       "agent-sandbox",
						TimeoutSeconds: 3600,
					},
				},
			},
			wantErrs: 0,
		},
		{
			name: "blocked namespace",
			task: &AgentTask{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fix-bug-3",
					Namespace: "kube-system",
				},
				Spec: AgentTaskSpec{
					Objective: "Fix NPE",
					Repository: AgentTaskRepository{
						URL: "https://github.com/org/repo",
					},
				},
			},
			wantErrs:  1,
			wantField: "metadata.namespace",
		},
		{
			name: "empty objective",
			task: &AgentTask{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fix-bug-4",
					Namespace: "default",
				},
				Spec: AgentTaskSpec{
					Objective: "   ",
					Repository: AgentTaskRepository{
						URL: "https://github.com/org/repo",
					},
				},
			},
			wantErrs:  1,
			wantField: "spec.objective",
		},
		{
			name: "missing repository URL",
			task: &AgentTask{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fix-bug-5",
					Namespace: "default",
				},
				Spec: AgentTaskSpec{
					Objective:  "Fix NPE",
					Repository: AgentTaskRepository{},
				},
			},
			wantErrs:  1,
			wantField: "spec.repository.url",
		},
		{
			name: "invalid repository URL scheme",
			task: &AgentTask{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fix-bug-6",
					Namespace: "default",
				},
				Spec: AgentTaskSpec{
					Objective: "Fix NPE",
					Repository: AgentTaskRepository{
						URL: "file:///etc/passwd",
					},
				},
			},
			wantErrs:  1,
			wantField: "spec.repository.url",
		},
		{
			name: "non-numeric budgetUSD",
			task: &AgentTask{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fix-bug-7",
					Namespace: "default",
				},
				Spec: AgentTaskSpec{
					Objective: "Fix NPE",
					Repository: AgentTaskRepository{
						URL: "https://github.com/org/repo",
					},
					BudgetUSD: "abc",
				},
			},
			wantErrs:  1,
			wantField: "spec.budgetUSD",
		},
		{
			name: "negative budgetUSD",
			task: &AgentTask{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fix-bug-8",
					Namespace: "default",
				},
				Spec: AgentTaskSpec{
					Objective: "Fix NPE",
					Repository: AgentTaskRepository{
						URL: "https://github.com/org/repo",
					},
					BudgetUSD: "-5.00",
				},
			},
			wantErrs:  1,
			wantField: "spec.budgetUSD",
		},
		{
			name: "zero budgetUSD",
			task: &AgentTask{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fix-bug-9",
					Namespace: "default",
				},
				Spec: AgentTaskSpec{
					Objective: "Fix NPE",
					Repository: AgentTaskRepository{
						URL: "https://github.com/org/repo",
					},
					BudgetUSD: "0",
				},
			},
			wantErrs:  1,
			wantField: "spec.budgetUSD",
		},
		{
			name: "negative budgetTokens",
			task: &AgentTask{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fix-bug-10",
					Namespace: "default",
				},
				Spec: AgentTaskSpec{
					Objective: "Fix NPE",
					Repository: AgentTaskRepository{
						URL: "https://github.com/org/repo",
					},
					BudgetTokens: -100,
				},
			},
			wantErrs:  1,
			wantField: "spec.budgetTokens",
		},
		{
			name: "negative maxIterations and timeoutSeconds",
			task: &AgentTask{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fix-bug-11",
					Namespace: "default",
				},
				Spec: AgentTaskSpec{
					Objective: "Fix NPE",
					Repository: AgentTaskRepository{
						URL: "https://github.com/org/repo",
					},
					MaxIterations: -1,
					Sandbox: AgentTaskSandbox{
						TimeoutSeconds: -60,
					},
				},
			},
			wantErrs:  2,
			wantField: "spec.maxIterations",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateAgentTask(tt.task)
			if len(errs) != tt.wantErrs {
				t.Errorf("got %d errors, want %d:\n%v", len(errs), tt.wantErrs, errs)
				return
			}
			if tt.wantField != "" && len(errs) > 0 {
				found := false
				for _, e := range errs {
					if contains(e.Field, tt.wantField) || contains(e.Detail, tt.wantField) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error mentioning %q, got: %v", tt.wantField, errs)
				}
			}
		})
	}
}

func TestAgentTaskValidatorLifecycle(t *testing.T) {
	v := &agentTaskValidator{}
	ctx := context.Background()

	validTask := &AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-1",
			Namespace: "default",
		},
		Spec: AgentTaskSpec{
			Objective: "Fix bug",
			Repository: AgentTaskRepository{
				URL: "https://github.com/org/repo",
			},
		},
	}

	if _, err := v.ValidateCreate(ctx, validTask); err != nil {
		t.Errorf("ValidateCreate on valid task returned error: %v", err)
	}

	invalidTask := &AgentTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-1",
			Namespace: "istio-system",
		},
		Spec: AgentTaskSpec{},
	}

	if _, err := v.ValidateCreate(ctx, invalidTask); err == nil {
		t.Error("ValidateCreate on invalid task expected error, got nil")
	}

	if _, err := v.ValidateUpdate(ctx, validTask, invalidTask); err == nil {
		t.Error("ValidateUpdate to invalid task expected error, got nil")
	}

	if _, err := v.ValidateUpdate(ctx, validTask, validTask); err != nil {
		t.Errorf("ValidateUpdate to valid task returned error: %v", err)
	}

	if _, err := v.ValidateDelete(ctx, invalidTask); err != nil {
		t.Errorf("ValidateDelete returned error: %v", err)
	}

	now := metav1.Now()
	deletingInvalidTask := invalidTask.DeepCopy()
	deletingInvalidTask.DeletionTimestamp = &now
	if _, err := v.ValidateUpdate(ctx, invalidTask, deletingInvalidTask); err != nil {
		t.Errorf("ValidateUpdate on deleting task returned error: %v", err)
	}
}
