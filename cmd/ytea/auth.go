package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/omegaatt36/ytea/internal/googleauth"
)

func authCommand(opts *options, root *cli.Command) *cli.Command {
	return &cli.Command{
		Name:  "auth",
		Usage: "manage Google account authorization",
		Commands: []*cli.Command{
			{
				Name:  "login",
				Usage: "authorize this device with Google",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if err := loadOptions(root); err != nil {
						return err
					}
					return authLogin(ctx, *opts, cmd.Writer, googleauth.Config{})
				},
			},
			{
				Name:  "logout",
				Usage: "remove the locally stored Google token",
				Action: func(_ context.Context, cmd *cli.Command) error {
					return authLogout(cmd.Writer)
				},
			},
		},
	}
}

func authLogin(ctx context.Context, opts options, output io.Writer, config googleauth.Config) error {
	const clientSetup = "create a Google OAuth client of type \"TVs and Limited Input devices\""
	if strings.TrimSpace(opts.googleClientID) == "" {
		return fmt.Errorf("google-client-id is required; %s", clientSetup)
	}
	if strings.TrimSpace(opts.googleClientSecret) == "" {
		return fmt.Errorf("google-client-secret is required; %s", clientSetup)
	}
	config.ClientID = opts.googleClientID
	config.ClientSecret = opts.googleClientSecret
	client, err := googleauth.NewClient(config)
	if err != nil {
		return fmt.Errorf("configure Google authorization: %w", err)
	}
	if _, err := client.Login(ctx, output); err != nil {
		return fmt.Errorf("authorize Google account: %w", err)
	}
	return nil
}

func authLogout(output io.Writer) error {
	path, err := googleauth.TokenPath()
	if err != nil {
		return fmt.Errorf("find Google token: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove Google token: %w", err)
	}
	if _, err := fmt.Fprintln(output, "Local Google token removed. Revoke ytea access at https://myaccount.google.com/connections (third-party access)."); err != nil {
		return fmt.Errorf("display Google revoke instructions: %w", err)
	}
	return nil
}
