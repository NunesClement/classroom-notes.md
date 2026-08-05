package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/clementnunes/sage-resilient-urgent-scheduler/pkg/intent"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, "intentctl:", err)
		os.Exit(1)
	}
}

func run(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	getenv func(string) string,
) error {
	flags := flag.NewFlagSet("intentctl", flag.ContinueOnError)
	flags.SetOutput(stderr)
	endpoint := flags.String(
		"endpoint",
		getenv("HERMES_CHAT_COMPLETIONS_URL"),
		"full OpenAI-compatible Hermes chat-completions URL",
	)
	modelDefault := strings.TrimSpace(getenv("HERMES_MODEL"))
	if modelDefault == "" {
		modelDefault = "glm-5.2"
	}
	model := flags.String("model", modelDefault, "Hermes model name")
	inputPath := flags.String("input", "-", "intent text file, or - for stdin")
	textValue := flags.String("text", "", "intent text supplied directly")
	jsonMode := flags.Bool("json-mode", true, "request JSON-object response mode from Hermes")
	timeout := flags.Duration("timeout", 2*time.Minute, "maximum translation time")
	pretty := flags.Bool("pretty", true, "pretty-print the resulting JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	if *timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	if strings.TrimSpace(*textValue) != "" && *inputPath != "-" {
		return errors.New("use either -text or -input, not both")
	}

	source, err := readIntent(*textValue, *inputPath, stdin)
	if err != nil {
		return err
	}
	requestContext, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	httpClient := &http.Client{Timeout: *timeout}
	client, err := intent.NewOpenAICompatibleClient(intent.OpenAICompatibleConfig{
		Endpoint:   *endpoint,
		Model:      *model,
		APIKey:     getenv("HERMES_API_KEY"),
		JSONMode:   *jsonMode,
		HTTPClient: httpClient,
	})
	if err != nil {
		return err
	}
	translator, err := intent.NewTranslator(client)
	if err != nil {
		return err
	}
	draft, err := translator.Translate(requestContext, source)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	if *pretty {
		encoder.SetIndent("", "  ")
	}
	if err := encoder.Encode(draft); err != nil {
		return fmt.Errorf("encode intent draft: %w", err)
	}
	return nil
}

func readIntent(textValue, inputPath string, stdin io.Reader) (string, error) {
	if strings.TrimSpace(textValue) != "" {
		return boundedText(strings.NewReader(textValue))
	}
	input := stdin
	if inputPath != "-" {
		file, err := os.Open(inputPath)
		if err != nil {
			return "", fmt.Errorf("open intent input: %w", err)
		}
		defer file.Close()
		input = file
	}
	return boundedText(input)
}

func boundedText(reader io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(reader, intent.MaxSourceBytes+1))
	if err != nil {
		return "", fmt.Errorf("read intent input: %w", err)
	}
	if len(data) > intent.MaxSourceBytes {
		return "", fmt.Errorf("intent input exceeds %d bytes", intent.MaxSourceBytes)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "", errors.New("intent input is empty")
	}
	return text, nil
}
