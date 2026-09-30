package executor

import (
	"strings"
	"testing"
)

func TestExecuteCPP(t *testing.T) {
	req := PipelineRequest{
		Language: "cpp",
		Code: `#include <iostream>
int main() {
    std::cout << "HELLO_CPP_AXIOMBOX" << std::endl;
    return 0;
}`,
		MemoryLimitMB: 64,
		TimeLimitMs:   3000,
	}

	res, err := Execute(req)
	if err != nil {
		t.Fatalf("Execute C++ failed: %v", err)
	}

	if res.Status != "COMPLETED" {
		t.Errorf("Expected COMPLETED, got %s. Stderr: %s", res.Status, res.Stderr)
	}

	if !strings.Contains(res.Stdout, "HELLO_CPP_AXIOMBOX") {
		t.Errorf("Expected stdout to contain 'HELLO_CPP_AXIOMBOX', got: %s", res.Stdout)
	}

	t.Logf("C++ executed successfully in %d ms (compile: %d ms, peak memory: %.2f MB)",
		res.ExecutionTimeMs, res.CompileTimeMs, res.PeakMemoryMB)
}

func TestExecutePython(t *testing.T) {
	req := PipelineRequest{
		Language: "python",
		Code: `print("HELLO_PYTHON_AXIOMBOX")`,
		MemoryLimitMB: 64,
		TimeLimitMs:   3000,
	}

	res, err := Execute(req)
	if err != nil {
		t.Fatalf("Execute Python failed: %v", err)
	}

	if res.Status != "COMPLETED" {
		t.Errorf("Expected COMPLETED, got %s. Stderr: %s", res.Status, res.Stderr)
	}

	if !strings.Contains(res.Stdout, "HELLO_PYTHON_AXIOMBOX") {
		t.Errorf("Expected stdout to contain 'HELLO_PYTHON_AXIOMBOX', got: %s", res.Stdout)
	}

	t.Logf("Python executed successfully in %d ms (peak memory: %.2f MB)",
		res.ExecutionTimeMs, res.PeakMemoryMB)
}

func TestExecuteJavaScript(t *testing.T) {
	req := PipelineRequest{
		Language: "javascript",
		Code: `console.log("HELLO_JS_AXIOMBOX");`,
		MemoryLimitMB: 64,
		TimeLimitMs:   3000,
	}

	res, err := Execute(req)
	if err != nil {
		t.Fatalf("Execute JavaScript failed: %v", err)
	}

	if res.Status != "COMPLETED" {
		t.Errorf("Expected COMPLETED, got %s. Stderr: %s", res.Status, res.Stderr)
	}

	if !strings.Contains(res.Stdout, "HELLO_JS_AXIOMBOX") {
		t.Errorf("Expected stdout to contain 'HELLO_JS_AXIOMBOX', got: %s", res.Stdout)
	}

	t.Logf("JavaScript executed successfully in %d ms (peak memory: %.2f MB)",
		res.ExecutionTimeMs, res.PeakMemoryMB)
}

func TestExecuteJava(t *testing.T) {
	req := PipelineRequest{
		Language: "java",
		Code: `public class Main {
    public static void main(String[] args) {
        System.out.println("HELLO_JAVA_AXIOMBOX");
    }
}`,
		MemoryLimitMB: 128,
		TimeLimitMs:   4000,
	}

	res, err := Execute(req)
	if err != nil {
		t.Fatalf("Execute Java failed: %v", err)
	}

	if res.Status != "COMPLETED" {
		t.Errorf("Expected COMPLETED, got %s. Stderr: %s", res.Status, res.Stderr)
	}

	if !strings.Contains(res.Stdout, "HELLO_JAVA_AXIOMBOX") {
		t.Errorf("Expected stdout to contain 'HELLO_JAVA_AXIOMBOX', got: %s", res.Stdout)
	}

	t.Logf("Java executed successfully in %d ms (compile: %d ms, peak memory: %.2f MB)",
		res.ExecutionTimeMs, res.CompileTimeMs, res.PeakMemoryMB)
}

func TestExecuteCSharp(t *testing.T) {
	req := PipelineRequest{
		Language: "csharp",
		Code: `using System;
class Program {
    static void Main() {
        Console.WriteLine("HELLO_CSHARP_AXIOMBOX");
    }
}`,
		MemoryLimitMB: 64,
		TimeLimitMs:   3000,
	}

	res, err := Execute(req)
	if err != nil {
		t.Fatalf("Execute C# failed: %v", err)
	}

	if res.Status != "COMPLETED" {
		t.Errorf("Expected COMPLETED, got %s. Stderr: %s", res.Status, res.Stderr)
	}

	if !strings.Contains(res.Stdout, "HELLO_CSHARP_AXIOMBOX") {
		t.Errorf("Expected stdout to contain 'HELLO_CSHARP_AXIOMBOX', got: %s", res.Stdout)
	}

	t.Logf("C# executed successfully in %d ms (compile: %d ms, peak memory: %.2f MB)",
		res.ExecutionTimeMs, res.CompileTimeMs, res.PeakMemoryMB)
}

