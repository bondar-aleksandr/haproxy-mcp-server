package testing

import (
	"context"
	"fmt"
)

// MockRuntimeClient implements the RuntimeClient interface for testing
type MockRuntimeClient struct {
	// Configuration for mocking behavior
	FailExecuteCommand bool
	FailGetProcessInfo bool

	// Mocked return values
	CommandResponses map[string]string
	ProcessInfo      map[string]string

	// Record method calls for verification
	ExecutedCommands []string
}

// NewMockRuntimeClient creates a new mock runtime client with default settings
func NewMockRuntimeClient() *MockRuntimeClient {
	return &MockRuntimeClient{
		CommandResponses: make(map[string]string),
		ProcessInfo:      map[string]string{"version": "2.4.0"},
	}
}

// ExecuteRuntimeCommand implements RuntimeClient.ExecuteRuntimeCommand
func (m *MockRuntimeClient) ExecuteRuntimeCommand(command string) (string, error) {
	m.ExecutedCommands = append(m.ExecutedCommands, command)

	if m.FailExecuteCommand {
		return "", fmt.Errorf("mock error executing command: %s", command)
	}

	if response, exists := m.CommandResponses[command]; exists {
		return response, nil
	}

	return "mock_response", nil
}

// ExecuteRuntimeCommandWithContext implements RuntimeClient.ExecuteRuntimeCommandWithContext
func (m *MockRuntimeClient) ExecuteRuntimeCommandWithContext(ctx context.Context, command string) (string, error) {
	// Check if context is already canceled
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// Delegate to the non-context version
	return m.ExecuteRuntimeCommand(command)
}

// GetProcessInfo implements RuntimeClient.GetProcessInfo
func (m *MockRuntimeClient) GetProcessInfo() (map[string]string, error) {
	if m.FailGetProcessInfo {
		return nil, fmt.Errorf("mock error getting process info")
	}
	return m.ProcessInfo, nil
}

// GetProcessInfoWithContext implements RuntimeClient.GetProcessInfoWithContext
func (m *MockRuntimeClient) GetProcessInfoWithContext(ctx context.Context) (map[string]string, error) {
	// Check if context is already canceled
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Delegate to the non-context version
	return m.GetProcessInfo()
}

// Close implements RuntimeClient.Close
func (m *MockRuntimeClient) Close() error {
	return nil
}
