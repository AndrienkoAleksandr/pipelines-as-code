package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/spf13/cobra"
)

const (
	pacNS                  = "pipelines-as-code"
	openshiftpacNS         = "openshift-pipelines"
	openShiftRouteGroup    = "route.openshift.io"
	openShiftRouteVersion  = "v1"
	openShiftRouteResource = "routes"
	secretName             = "pipelines-as-code-secret"
	defaultProviderType    = "github-app"
)

var providerTargets = []string{"github-app", "github-enterprise-app"}

type bootstrapOpts struct {
	providerType    string
	installNightly  bool
	skipInstall     bool
	skipGithubAPP   bool
	forceInstall    bool
	webserverPort   int
	cliOpts         *cli.PacCliOpts
	ioStreams       *cli.IOStreams
	targetNamespace string

	RouteName              string
	GithubAPIURL           string
	GithubApplicationName  string
	GithubApplicationURL   string
	GithubOrganizationName string
	forceGitHubApp         bool
}

const indexTmpl = `
<html>
<body>
  <form method="post" action="%s/settings/apps/new">
  <input type="submit" value="Create your GitHub APP"></input>
  <input type="hidden" name="manifest" value='%s'"/>
  </form>
</body>
</html>
`

const successTmpl = `
<html><body>You have <span style=\"color: green\">successfully</span> created a new GitHub application, go back to the tkn pac cli to finish the installation.</body></html>
`

func install(ctx context.Context, run *params.Run, opts *bootstrapOpts) error {
	if !opts.forceInstall {
		fmt.Fprintln(opts.ioStreams.Out, "🏃 Checking if Pipelines as Code is installed.")
	}
	tektonInstalled, err := checkPipelinesInstalled(run)
	if err != nil {
		return err
	}
	if !tektonInstalled {
		return errors.New("tekton API not found on the cluster. Please install Tekton first")
	}

	// if we gt a ns back it means it has been detected in here so keep it as is.
	// or else just set the default to pacNS
	ns, err := DetectPacInstallation(ctx, opts.targetNamespace, run)
	if ns != "" {
		opts.targetNamespace = ns
	} else if opts.targetNamespace == "" {
		opts.targetNamespace = pacNS
	}

	if !opts.forceInstall && err == nil {
		fmt.Fprintln(opts.ioStreams.Out, "👌 Pipelines as Code is already installed.")
	} else if err := installPac(ctx, run, opts); err != nil {
		return err
	}
	return nil
}

func createSecret(ctx context.Context, run *params.Run, opts *bootstrapOpts) error {
	var err error

	if opts.RouteName == "" {
		opts.RouteName, _ = DetectOpenShiftRoute(ctx, run, opts.targetNamespace)
	}
	if err := askQuestions(opts); err != nil {
		return err
	}

	if opts.forceGitHubApp {
		if err := deleteSecret(ctx, run, opts); err != nil {
			return err
		}
	}

	jeez, err := generateManifest(opts)
	if err != nil {
		return err
	}

	return startWebServer(ctx, opts, run, string(jeez))
}

func Command(run *params.Run, ioStreams *cli.IOStreams) *cobra.Command {
	opts := &bootstrapOpts{
		ioStreams: ioStreams,
	}
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Long:  "Bootstrap Pipelines as Code",
		Short: "Bootstrap Pipelines as Code.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			opts.cliOpts = cli.NewCliOptions(cmd)
			opts.ioStreams.SetColorEnabled(!opts.cliOpts.NoColoring)
			if err := run.Clients.NewClients(ctx, &run.Info); err != nil {
				return err
			}

			if !opts.skipInstall {
				if err := install(ctx, run, opts); err != nil {
					return err
				}
			}

			pacInfo, err := info.GetPACInfo(ctx, run, opts.targetNamespace)
			if err != nil {
				return err
			}

			if !opts.forceGitHubApp {
				if pacInfo.Provider == provider.ProviderGitHubApp {
					fmt.Fprintln(opts.ioStreams.Out, "👌 Skips bootstrapping GitHub App, as one is already configured. Please pass --force-configure to override existing")
					return nil
				}
			}

			if !opts.skipGithubAPP {
				if err := createSecret(ctx, run, opts); err != nil {
					return err
				}
			}
			return nil
		},
		Annotations: map[string]string{
			"commandType": "main",
		},
	}
	cmd.AddCommand(GithubApp(run, ioStreams))

	addCommonFlags(cmd, ioStreams)
	addGithubAppFlag(cmd, opts)

	cmd.PersistentFlags().BoolVar(&opts.forceInstall, "force-install", false, "whether we should force pac install even if it's already installed")
	cmd.PersistentFlags().BoolVar(&opts.skipInstall, "skip-install", false, "skip Pipelines as Code installation")
	cmd.PersistentFlags().BoolVar(&opts.skipGithubAPP, "skip-github-app", false, "skip creating github application")

	return cmd
}

func GithubApp(run *params.Run, ioStreams *cli.IOStreams) *cobra.Command {
	opts := &bootstrapOpts{
		ioStreams: ioStreams,
	}

	cmd := &cobra.Command{
		Use:   "github-app",
		Long:  "A command helper to help you create the Pipelines as Code GitHub Application",
		Short: "Create PAC GitHub Application",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			opts.cliOpts = cli.NewCliOptions(cmd)
			opts.ioStreams.SetColorEnabled(!opts.cliOpts.NoColoring)
			if err := run.Clients.NewClients(ctx, &run.Info); err != nil {
				return err
			}

			var err error
			opts.targetNamespace, err = DetectPacInstallation(ctx, opts.targetNamespace, run)
			if err != nil {
				return err
			}

			pacInfo, err := info.GetPACInfo(ctx, run, opts.targetNamespace)
			if err != nil {
				return err
			}

			if !opts.forceGitHubApp {
				if pacInfo.Provider == provider.ProviderGitHubApp {
					fmt.Fprintln(opts.ioStreams.Out, "👌 Skips bootstrapping GitHub App, as one is already configured. Please pass --force-configure to override existing")
					return nil
				}
			}

			if b, _ := askYN(false, "", "Are you using GitHub Enterprise?", opts.ioStreams.Out); b {
				opts.providerType = "github-enterprise-app"
			}

			return createSecret(ctx, run, opts)
		},
		Annotations: map[string]string{
			"commandType": "main",
		},
	}
	addCommonFlags(cmd, ioStreams)
	addGithubAppFlag(cmd, opts)

	cmd.PersistentFlags().StringVarP(&opts.targetNamespace, "namespace", "n", "", "target namespace where pac is installed")
	return cmd
}

func DetectPacInstallation(ctx context.Context, wantedNS string, run *params.Run) (string, error) {
	// detect which namespace pac is installed in
	// verify first if the targetNamespace actually exists
	if wantedNS != "" {
		installed, err := checkNS(ctx, run, wantedNS)
		if err != nil {
			return "", err
		}
		if !installed {
			return "", fmt.Errorf("PAC is not installed in namespace: %s", wantedNS)
		}
		return wantedNS, nil
		// if openshift pipelines ns is installed try it from there
	}

	if installed, _ := checkNS(ctx, run, openshiftpacNS); installed {
		return openshiftpacNS, nil
	}

	if installed, _ := checkNS(ctx, run, pacNS); installed {
		return pacNS, nil
	}

	return "", fmt.Errorf("could not detect an installation of Pipelines as Code, " +
		"use the -n switch to specify a namespace")
}

func addGithubAppFlag(cmd *cobra.Command, opts *bootstrapOpts) {
	cmd.PersistentFlags().StringVar(&opts.GithubOrganizationName, "github-organization-name", "", "Whether you want to target an organization instead of the current user")
	cmd.PersistentFlags().StringVar(&opts.GithubApplicationName, "github-application-name", "", "GitHub Application Name")
	cmd.PersistentFlags().StringVar(&opts.GithubApplicationURL, "github-application-url", "", "GitHub Application URL")
	cmd.PersistentFlags().StringVarP(&opts.GithubAPIURL, "github-api-url", "", "", "Github Enterprise API URL")
	cmd.PersistentFlags().StringVar(&opts.RouteName, "route-url", "", "The public URL for the pipelines-as-code controller")
	cmd.PersistentFlags().BoolVar(&opts.installNightly, "nightly", false, "Whether to install the nightly Pipelines as Code")
	cmd.PersistentFlags().IntVar(&opts.webserverPort, "webserver-port", 8080, "Webserver port")
	cmd.PersistentFlags().StringVarP(&opts.providerType, "install-type", "t", defaultProviderType,
		fmt.Sprintf("target install type, choices are: %s ", strings.Join(providerTargets, ", ")))
	cmd.PersistentFlags().BoolVar(&opts.forceGitHubApp, "force-configure", false, "Whether we should override existing GitHub App")
}

func addCommonFlags(cmd *cobra.Command, ioStreams *cli.IOStreams) {
	cmd.PersistentFlags().BoolP("no-color", "C", !ioStreams.ColorEnabled(), "disable coloring")
}
