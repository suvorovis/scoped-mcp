package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"scoped-mcp/mcp-confluence/config"
	"scoped-mcp/mcp-confluence/confluence"
	"scoped-mcp/mcp-confluence/guard"
	"scoped-mcp/mcp-confluence/tools"
)

const version = "0.1.0"

var errConfigPathNotAbsolute = errors.New("serve: config path must be absolute")

func runServe(ctx context.Context, cfg config.Config, configPath string, tokenStore config.TokenStore, streams stdio) error {
	token, err := config.LoadToken(tokenStore.Reader)
	if err != nil {
		return err
	}
	mcpServer, err := newServer(cfg, configPath, token, http.DefaultTransport, streams.errOut)
	if err != nil {
		return err
	}
	return ignoreCancellation(mcpServer.Run(ctx, &mcp.StdioTransport{}))
}

func newServer(cfg config.Config, configPath, token string, nextTransport http.RoundTripper, logOutput io.Writer) (*mcp.Server, error) {
	if !filepath.IsAbs(configPath) {
		return nil, fmt.Errorf("%w: %q", errConfigPathNotAbsolute, configPath)
	}
	guardTransport, err := guard.New(cfg, nextTransport)
	if err != nil {
		return nil, err
	}
	confluenceClient, err := confluence.New(cfg.BaseURL, token, guardTransport, log.New(logOutput, "mcp-confluence: ", log.LstdFlags))
	if err != nil {
		return nil, err
	}
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "mcp-confluence", Version: version}, nil)
	tools.New(confluenceClient, confluenceClient, confluenceClient, cfg, configPath).Register(mcpServer)
	return mcpServer, nil
}

func ignoreCancellation(err error) error {
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
