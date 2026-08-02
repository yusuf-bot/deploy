package state

import "fmt"

// MergeEnvMap returns a new env map with secrets overriding appEnv on
// conflict. Secrets take precedence over deploy.yml `env:` and DB app env.
func MergeEnvMap(appEnv, secrets map[string]string) map[string]string {
	merged := make(map[string]string, len(appEnv)+len(secrets))
	for k, v := range appEnv {
		merged[k] = v
	}
	for k, v := range secrets {
		merged[k] = v
	}
	return merged
}

// MergeEnv combines app env vars with decrypted secrets into KEY=VALUE pairs.
// Secrets override app env on conflict.
func MergeEnv(appEnv, secrets map[string]string) []string {
	merged := MergeEnvMap(appEnv, secrets)
	env := make([]string, 0, len(merged))
	for k, v := range merged {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	return env
}
