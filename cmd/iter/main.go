package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/iter-dev/iter/internal/localauth"
)

func main() {
	ctx := context.Background()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "login":
		return login(ctx, args[1:])
	case "logout":
		return logout()
	case "whoami":
		return whoami()
	default:
		return usage()
	}
}

func login(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("login", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	noBrowser := flags.Bool("no-browser", false, "print the verification URL without opening a browser")
	if err := flags.Parse(args); err != nil {
		return err
	}

	client := localauth.NewClient(localauth.ConfigFromEnv())
	auth, err := client.AuthorizeDevice(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("Open %s and enter code %s\n", auth.VerificationURI, auth.UserCode)
	if auth.VerificationURIComplete != "" {
		fmt.Printf("One-click URL: %s\n", auth.VerificationURIComplete)
		if !*noBrowser {
			_ = openBrowser(auth.VerificationURIComplete)
		}
	}
	fmt.Println("Waiting for authorization...")

	tokens, err := client.PollToken(ctx, auth)
	if err != nil {
		return err
	}
	if err := localauth.SaveTokens(localauth.StoredTokens{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		IDToken:      tokens.IDToken,
	}); err != nil {
		return err
	}
	claims, err := localauth.DecodeClaims(tokens.AccessToken)
	if err != nil {
		return err
	}
	name := claims.DisplayName
	if name == "" {
		name = claims.Subject
	}
	fmt.Printf("Signed in as %s", name)
	if claims.TenantID != "" {
		fmt.Printf(" for tenant %s", claims.TenantID)
	}
	fmt.Println()
	return nil
}

func logout() error {
	if err := localauth.ClearTokens(); err != nil {
		return err
	}
	fmt.Println("Signed out")
	return nil
}

func whoami() error {
	tokens, err := localauth.LoadTokens()
	if err != nil {
		return err
	}
	claims, err := localauth.DecodeClaims(tokens.AccessToken)
	if err != nil {
		return err
	}
	name := claims.DisplayName
	if name == "" {
		name = claims.Subject
	}
	fmt.Printf("User: %s\n", name)
	fmt.Printf("Subject: %s\n", claims.Subject)
	if claims.TenantID != "" {
		fmt.Printf("Tenant: %s\n", claims.TenantID)
	}
	fmt.Printf("Expires: %s\n", claims.ExpiresAt.Format(time.RFC3339))
	return nil
}

func usage() error {
	return errors.New("usage: iter login [--no-browser] | logout | whoami")
}

func openBrowser(url string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	return exec.Command("open", url).Run()
}
