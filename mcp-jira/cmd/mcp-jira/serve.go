package main

import (
	"context"
	"errors"
	"net/http"
	"os/signal"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"scoped-mcp/mcp-jira/config"
	"scoped-mcp/mcp-jira/guard"
	"scoped-mcp/mcp-jira/jira"
	"scoped-mcp/mcp-jira/tools"
)

const version = "0.1.0"

func runServe(ctx context.Context, cfg config.Config, configPath string, tokenStore config.TokenStore, _ stdio) error {
	context.AfterFunc(ctx, restoreDefaultShutdownHandling)
	token, err := config.LoadToken(tokenStore.TokenReader)
	if err != nil {
		return err
	}
	mcpServer, err := newServer(cfg, configPath, token, http.DefaultTransport)
	if err != nil {
		return err
	}
	return ignoreCancellation(mcpServer.Run(ctx, &mcp.StdioTransport{}))
}

func newServer(cfg config.Config, configPath, token string, nextTransport http.RoundTripper) (*mcp.Server, error) {
	guardTransport, err := guard.New(cfg, nextTransport)
	if err != nil {
		return nil, err
	}
	jiraClient := jira.New(cfg.BaseURL, token, guardTransport)

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "mcp-jira", Version: version}, nil)
	clients := tools.Clients{Creator: jiraClient, Attacher: jiraClient, Commenter: jiraClient, Linker: jiraClient, Updater: jiraClient}
	tools.New(clients, cfg, configPath).Register(mcpServer)
	return mcpServer, nil
}

func restoreDefaultShutdownHandling() {
	signal.Reset(shutdownSignals...)
}

func ignoreCancellation(err error) error {
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
