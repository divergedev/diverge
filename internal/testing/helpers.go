package testing

import "github.com/divergedev/diverge/api/v1alpha1"

func headerKey(env *v1alpha1.Environment) string {
	if env.Spec.Routing.HeaderKey != "" {
		return env.Spec.Routing.HeaderKey
	}
	return "x-diverge-env"
}

func headerValue(env *v1alpha1.Environment) string {
	if env.Spec.Routing.HeaderValue != "" {
		return env.Spec.Routing.HeaderValue
	}
	return env.Name
}
