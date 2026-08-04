package state

import "fmt"

// MergeEnvMap returns a new env map with secrets overriding group env overriding appEnv on
// conflict. Order of precedence: appEnv < groupEnv < secrets.
func MergeEnvMap(appEnv, groupEnv, secrets map[string]string) map[string]string {
	merged := make(map[string]string, len(appEnv)+len(groupEnv)+len(secrets))
	for k, v := range appEnv {
		merged[k] = v
	}
	for k, v := range groupEnv {
		merged[k] = v
	}
	for k, v := range secrets {
		merged[k] = v
	}
	return merged
}

// MergeEnv combines app env vars with group env and decrypted secrets into KEY=VALUE pairs.
// Order of precedence: appEnv < groupEnv < secrets.
func MergeEnv(appEnv, groupEnv, secrets map[string]string) []string {
	merged := MergeEnvMap(appEnv, groupEnv, secrets)
	env := make([]string, 0, len(merged))
	for k, v := range merged {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	return env
}
