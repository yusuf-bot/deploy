package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"deploy/internal/config"
	"deploy/internal/types"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var scaffoldDir string

// dockerfileTemplates returns the Dockerfile content for the given stack.
func dockerfileTemplate(stack string, entryPoint string) string {
	switch stack {
	case "go":
		return `FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o app .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/app .
EXPOSE 8080
CMD ["./app"]
`
	case "node":
		return fmt.Sprintf(`FROM node:20-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm ci --only=production
COPY . .

FROM node:20-alpine
WORKDIR /app
COPY --from=builder /app .
EXPOSE 3000
CMD ["node", %q]
`, entryPoint)
	case "python":
		return `FROM python:3.12-slim AS builder
WORKDIR /app
COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt
COPY . .

FROM python:3.12-slim
WORKDIR /app
COPY --from=builder /app .
EXPOSE 8000
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"]
`
	case "static":
		return `FROM nginx:alpine
COPY . /usr/share/nginx/html
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
`
	}
	return ""
}

// stackPort returns the default container port for a given stack.
func stackPort(stack string) int {
	switch stack {
	case "go":
		return 8080
	case "node":
		return 3000
	case "python":
		return 8000
	case "static":
		return 80
	}
	return 0
}

// detectNodeEntry reads the "main" field from package.json, defaulting to "index.js".
func detectNodeEntry(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return "index.js"
	}
	var pkg struct {
		Main string `json:"main"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "index.js"
	}
	if pkg.Main == "" {
		return "index.js"
	}
	return pkg.Main
}

// sanitizeAppName converts a directory name to a valid app name.
func sanitizeAppName(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, "_", "-")
	return name
}

var scaffoldCmd = &cobra.Command{
	Use:   "scaffold",
	Short: "Scaffold deploy.yml and Dockerfile for a project",
	Long: `Auto-detect the application stack and generate deploy.yml
(and Dockerfile if one does not already exist).

Supported stacks: go, node, python, static.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runScaffold(scaffoldDir)
	},
}

func init() {
	scaffoldCmd.Flags().StringVar(&scaffoldDir, "dir", ".", "Target project directory")
	rootCmd.AddCommand(scaffoldCmd)
}

func runScaffold(dir string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve directory: %w", err)
	}

	// Check dir exists
	info, err := os.Stat(absDir)
	if err != nil {
		return fmt.Errorf("directory %q: %w", absDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", absDir)
	}

	stack := config.DetectStack(absDir)
	appName := sanitizeAppName(filepath.Base(absDir))

	switch stack {
	case "docker-compose":
		fmt.Println("Warning: docker-compose stacks not yet supported for scaffold")
		return nil

	case "unknown":
		fmt.Println("Could not auto-detect stack. Please create a Dockerfile manually, then run 'deploy scaffold' again.")
		return nil
	}

	hasDockerfile := stack == "dockerfile"
	genDockerfile := !hasDockerfile

	// Determine port
	port := stackPort(stack)
	if hasDockerfile {
		port = 8080 // default for existing Dockerfile
	}

	// Determine entry point for node
	var nodeEntry string
	if stack == "node" {
		nodeEntry = detectNodeEntry(absDir)
	}

	// Build deploy.yml
	cfg := types.DeployConfig{
		App:   appName,
		Stack: stack,
		Build: types.BuildConfig{
			Context: ".",
		},
		Ports: []types.PortMapping{
			{Container: port},
		},
		Health: types.HealthConfig{
			Path: "/health",
		},
		Domains: []string{},
	}

	ymlData, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal deploy config: %w", err)
	}

	// Preview
	fmt.Println("Deploy scaffold preview")
	fmt.Println("───────────────────────")
	fmt.Printf("Stack:  %s\n", stack)
	fmt.Printf("App:    %s\n", appName)
	fmt.Printf("Dir:    %s\n", absDir)
	fmt.Println()
	fmt.Println("Files to generate:")
	fmt.Printf("  - %s\n", filepath.Join(absDir, "deploy.yml"))
	if genDockerfile {
		fmt.Printf("  - %s\n", filepath.Join(absDir, "Dockerfile"))
	}
	fmt.Println()

	// Confirm
	fmt.Print("Continue? [Y/n]: ")
	var response string
	if _, err := fmt.Scanln(&response); err != nil {
		// EOF or non-interactive — default to yes
		response = "y"
	}
	if !strings.EqualFold(response, "y") && !strings.EqualFold(response, "yes") && response != "" {
		fmt.Println("Scaffold cancelled.")
		return nil
	}

	// Write deploy.yml
	ymlPath := filepath.Join(absDir, "deploy.yml")
	if err := os.WriteFile(ymlPath, ymlData, 0644); err != nil {
		return fmt.Errorf("write deploy.yml: %w", err)
	}
	fmt.Printf("Created %s\n", ymlPath)

	// Write Dockerfile if needed
	if genDockerfile {
		dfContent := dockerfileTemplate(stack, nodeEntry)
		if dfContent == "" {
			return fmt.Errorf("internal error: no dockerfile template for stack %q", stack)
		}
		dfPath := filepath.Join(absDir, "Dockerfile")
		if err := os.WriteFile(dfPath, []byte(dfContent), 0644); err != nil {
			return fmt.Errorf("write Dockerfile: %w", err)
		}
		fmt.Printf("Created %s\n", dfPath)
	}

	fmt.Println("Scaffold complete!")
	return nil
}
