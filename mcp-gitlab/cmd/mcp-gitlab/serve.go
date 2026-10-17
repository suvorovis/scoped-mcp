package main

import (
	"context"
	"errors"
	"net/http"
	"os/signal"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"scoped-mcp/mcp-gitlab/config"
	"scoped-mcp/mcp-gitlab/gitlab"
	"scoped-mcp/mcp-gitlab/guard"
	"scoped-mcp/mcp-gitlab/tools"
)

const version = "0.1.0"

func runServe(ctx context.Context, cfg config.Config, tokenStore config.TokenStore, _ stdio) error {
	context.AfterFunc(ctx, restoreDefaultShutdownHandling)
	token, err := config.LoadToken(tokenStore.TokenReader)
	if err != nil {
		return err
	}
	mcpServer, err := newServer(cfg, token, http.DefaultTransport)
	if err != nil {
		return err
	}
	return ignoreCancellation(mcpServer.Run(ctx, &mcp.StdioTransport{}))
}

func newServer(cfg config.Config, token string, nextTransport http.RoundTripper) (*mcp.Server, error) {
	guardTransport, err := guard.New(cfg, nextTransport)
	if err != nil {
		return nil, err
	}
	gitlabClient, err := gitlab.New(cfg.BaseURL, token, guardTransport)
	if err != nil {
		return nil, err
	}

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "mcp-gitlab", Version: version}, nil)
	tools.New(gitlabClient, cfg).Register(mcpServer)
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
