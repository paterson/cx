package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/cloud66/cloud66"

	"github.com/cloud66/cli"
	"github.com/jcoene/honeybadger"
	"github.com/mgutz/ansi"
)

type Command struct {
	Name       string
	Build      func() cli.Command
	Run        func(c *cli.Context)
	Flags      []cli.Flag
	Short      string
	Long       string
	NeedsStack bool
}

var (
	client       cloud66.Client
	debugMode    bool   = false
	VERSION      string = "dev"
	BUILD_DATE   string = ""
	tokenFile    string = "cx.json"
	fayeEndpoint string = "https://sockets.cloud66.com:443/push"
)

var commands = []*Command{
	cmdStacks,
	cmdRedeploy,
	cmdOpen,
	cmdSettings,
	cmdEasyDeploy,
	cmdEnvVars,
	cmdLease,
	cmdRun,
	cmdServers,
	cmdSsh,
	cmdTail,
	cmdUpload,
	cmdDownload,
	cmdBackups,
	cmdContainers,
	cmdServices,
	cmdDatabases,
	cmdHelpEnviron,
	cmdUpdate,
	cmdInfo,
}

var (
	flagStack       *cloud66.Stack
	flagEnvironment string
)

func main() {
	honeybadger.ApiKey = "09d82034"
	defer recoverPanic()

	cli.VersionPrinter = runVersion

	app := cli.NewApp()

	cmds := []cli.Command{}

	for _, cmd := range commands {

		cliCommand := cmd.Build()

		if cmd.Name == "" {
			printFatal("No Name is specified for %s", cmd)
		}

		cliCommand.Name = cmd.Name
		cliCommand.Usage = cmd.Short
		cliCommand.Description = cmd.Long
		cliCommand.Action = cmd.Run
		cliCommand.Flags = cmd.Flags

		if len(cliCommand.Subcommands) == 0 {
			if cmd.NeedsStack {
				cliCommand.Flags = append(cliCommand.Flags,
					cli.StringFlag{
						Name:  "stack,s",
						Usage: "full or partial stack name. This can be omitted if the current directory is a stack directory",
					}, cli.StringFlag{
						Name:  "environment,e",
						Usage: "full or partial environment name",
					})
			}
		} else {
			for idx, sub := range cliCommand.Subcommands {
				if cmd.NeedsStack {
					sub.Flags = append(sub.Flags,
						cli.StringFlag{
							Name:  "stack,s",
							Usage: "full or partial stack name. This can be omitted if the current directory is a stack directory",
						}, cli.StringFlag{
							Name:  "environment,e",
							Usage: "full or partial environment name",
						})
				}

				cliCommand.Subcommands[idx].Flags = sub.Flags
			}
		}

		cmds = append(cmds, cliCommand)
	}

	app.Commands = cmds
	app.Name = "cx"
	app.Usage = "Cloud 66 Command line toolbelt"
	app.Author = "Cloud 66"
	app.Email = "support@cloud66.com"
	app.Version = VERSION
	app.CommandNotFound = suggest
	app.Before = beforeCommand
	app.Action = doMain

	setGlobals(app)
	app.Run(os.Args)
}

func beforeCommand(c *cli.Context) error {
	// set the env vars from global options
	if c.GlobalString("runenv") != "production" {
		tokenFile = "cx_" + c.GlobalString("runenv") + ".json"
		fmt.Printf(ansi.Color(fmt.Sprintf("Running against %s environment\n", c.GlobalString("runenv")), "grey"))
		honeybadger.Environment = c.GlobalString("runenv")
	} else {
		honeybadger.Environment = "production"
	}

	if c.GlobalString("fayeEndpoint") != "" {
		fayeEndpoint = c.GlobalString("fayeEndpoint")
	}

	debugMode = c.GlobalBool("debug")

	var command string
	if len(c.Args()) >= 1 {
		command = c.Args().First()
	}

	if (command != "version") && (command != "help") && (command != "update") {
		initClients(c)
	}

	if (command != "update") && (VERSION != "dev") {
		defer backgroundRun()
	}

	return nil
}

func setGlobals(app *cli.App) {
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:   "runenv",
			Usage:  "sets the environment this toolbelt is running against",
			Value:  "production",
			EnvVar: "CXENVIRONMENT",
		},
		cli.StringFlag{
			Name:   "fayeEndpoint",
			Usage:  "sets the Faye endpoint this toolbelt is running against",
			EnvVar: "CX_FAYE_ENDPOINT",
		},
		cli.BoolFlag{
			Name:   "debug",
			Usage:  "run in debug mode",
			EnvVar: "CXDEBUG",
		},
	}
}

func buildBasicCommand() cli.Command {
	return cli.Command{}
}

func doMain(c *cli.Context) {
	cli.ShowAppHelp(c)
}

func initClients(c *cli.Context) {
	// is there a token file?
	_, err := os.Stat(filepath.Join(cxHome(), tokenFile))
	if err != nil {
		fmt.Println("No previous authentication found.")
		cloud66.Authorize(cxHome(), tokenFile)
		os.Exit(1)
	} else {
		client = cloud66.GetClient(cxHome(), tokenFile, VERSION)
		debugMode = c.GlobalBool("debug")
		client.Debug = debugMode
	}
}

func recoverPanic() {
	if VERSION != "dev" {
		if rec := recover(); rec != nil {
			report, err := honeybadger.NewReport(rec)
			if err != nil {
				printError("reporting crash failed: %s", err.Error())
				panic(rec)
			}
			report.AddContext("Version", VERSION)
			report.AddContext("Platform", runtime.GOOS)
			report.AddContext("Architecture", runtime.GOARCH)
			report.AddContext("DebugMode", debugMode)
			result := report.Send()
			if result != nil {
				printError("reporting crash failed: %s", result.Error())
				panic(rec)
			}
			printFatal("cx encountered and reported an internal client error")
		}
	}
}

func filterByEnvironmentExact(item interface{}) bool {
	if flagEnvironment == "" {
		return true
	}
	return strings.ToLower(item.(cloud66.Stack).Environment) == strings.ToLower(flagEnvironment)
}

func filterByEnvironmentFuzzy(item interface{}) bool {
	if flagEnvironment == "" {
		return true
	}
	return strings.HasPrefix(strings.ToLower(item.(cloud66.Stack).Environment), strings.ToLower(flagEnvironment))
}

func stack(c *cli.Context) (*cloud66.Stack, error) {
	if flagStack != nil {
		return flagStack, nil
	}

	if c.String("environment") != "" {
		flagEnvironment = c.String("environment")
	}

	var err error
	if c.String("stack") != "" {
		stacks, err := client.StackListWithFilter(filterByEnvironmentExact)
		if err != nil {
			return nil, err
		}
		var stackNames []string
		for _, stack := range stacks {
			stackNames = append(stackNames, stack.Name)
		}
		idx, err := fuzzyFind(stackNames, c.String("stack"), false)
		if err != nil {
			// try fuzzy env match
			stacks, err = client.StackListWithFilter(filterByEnvironmentFuzzy)
			if err != nil {
				return nil, err
			}
			var stackFuzzNames []string
			for _, stack := range stacks {
				stackFuzzNames = append(stackFuzzNames, stack.Name)
			}
			idx, err = fuzzyFind(stackFuzzNames, c.String("stack"), false)
			if err != nil {
				return nil, err
			}
		}

		flagStack = &stacks[idx]

		// toSdout is of type []bool. Take first value
		if c.String("environment") != "" {
			fmt.Printf("(%s)\n", flagStack.Environment)
		}

		return flagStack, err
	}

	if stack := c.String("cxstack"); stack != "" {
		// the environment variable should be exact match
		flagStack, err = client.StackInfo(stack)
		return flagStack, err
	}

	return stackFromGitRemote(remoteGitUrl(), localGitBranch())
}

func mustStack(c *cli.Context) *cloud66.Stack {
	stack, err := stack(c)
	if err != nil {
		printFatal(err.Error())
	}

	if stack == nil {
		printFatal("No stack specified. Either use --stack flag to cd to a stack directory")
	}

	return stack
}
