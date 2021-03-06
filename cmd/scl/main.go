package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Masterminds/vcs"
	"github.com/aryann/difflib"
	"github.com/tucnak/climax"

	"github.com/homemade/scl"
)

func main() {
	app := climax.New("scl")
	app.Brief = "Scl is a tool for managing SCL soure code."
	app.Version = "1.3.1"

	app.AddCommand(getCommand(os.Stdout, os.Stderr))
	app.AddCommand(runCommand(os.Stdout, os.Stderr))
	app.AddCommand(testCommand(os.Stdout, os.Stderr))

	os.Exit(app.Run())
}

func runCommand(stdout io.Writer, stderr io.Writer) climax.Command {

	return climax.Command{
		Name:  "run",
		Brief: "Transform one or more .scl files into HCL",
		Usage: `[options] <filename.scl...>`,
		Help:  `Transform one or more .scl files into HCL. Output is written to stdout.`,

		Flags: standardParserParams(),

		Handle: func(ctx climax.Context) int {

			if len(ctx.Args) == 0 {
				fmt.Fprintf(stderr, "At least one filename is required. See `sep help run` for syntax")
				return 1
			}

			params, includePaths := parserParams(ctx)

			for _, fileName := range ctx.Args {

				parser, err := scl.NewParser(scl.NewDiskSystem())

				if err != nil {
					fmt.Fprintf(stderr, "Error: Unable to create new parser in CWD: %s\n", err.Error())
					return 1
				}

				for _, includeDir := range includePaths {
					parser.AddIncludePath(includeDir)
				}

				for _, p := range params {
					parser.SetParam(p.name, p.value)
				}

				if err := parser.Parse(fileName); err != nil {
					fmt.Fprintf(stderr, "Error: Unable to parse file: %s\n", err.Error())
					return 1
				}

				fmt.Fprintf(stdout, "/* %s */\n%s\n\n", fileName, parser)
			}

			return 0
		},
	}
}

func getCommand(stdout io.Writer, stderr io.Writer) climax.Command {

	return climax.Command{
		Name:  "get",
		Brief: "Download libraries from verion control",
		Usage: `[options] <url...>`,
		Help:  "Get downloads the dependencies specified by the URLs provided, cloning or checking them out from their VCS.",

		Flags: []climax.Flag{
			{
				Name:     "output-path",
				Short:    "o",
				Usage:    `--output-path /my/vendor/path`,
				Help:     `The root path under which the dependencies will be stored. Default is "vendor".`,
				Variable: true,
			},
			{
				Name:  "update",
				Short: "u",
				Usage: `--update`,
				Help:  `Update existing repositories to their newest version`,
			},
			{
				Name:  "verbose",
				Short: "v",
				Usage: `--verbose`,
				Help:  `Print names of repositories as they are acquired or updated`,
			},
		},

		Handle: func(ctx climax.Context) int {

			if len(ctx.Args) == 0 {
				fmt.Fprintf(stderr, "At least one dependency is required. See `sep help get` for syntax")
				return 1
			}

			vendorDir := "vendor"

			if outputPath, set := ctx.Get("output-path"); set {
				vendorDir = outputPath
			}

			vendorDir, err := filepath.Abs(vendorDir)

			if err != nil {
				fmt.Fprintln(stderr, "Can't get path:", err.Error())
				return 1
			}

			newCount, updatedCount := 0, 0

			for _, dep := range ctx.Args {

				remote := fmt.Sprintf("https://%s", strings.TrimPrefix(dep, "https://"))
				path := filepath.Join(vendorDir, dep)

				if err := os.MkdirAll(path, os.ModeDir); err != nil {
					fmt.Fprintf(stderr, "Can't create path %s: %s\n", vendorDir, err.Error())
					return 1
				}

				repo, err := vcs.NewRepo(remote, path)

				if err != nil {
					fmt.Fprintf(stderr, "[%s] Can't create repo: %s", dep, err.Error())
					continue
				}

				if repo.CheckLocal() {

					if !ctx.Is("update") {
						if ctx.Is("verbose") {
							fmt.Fprintf(stderr, "[%s] already present, run with -u to update\n", dep)
						}
						continue
					}

					if err := repo.Update(); err != nil {
						fmt.Fprintf(stderr, "[%s] Can't update repo: %s\n", dep, err.Error())
						continue
					}

					updatedCount++

					if ctx.Is("verbose") {
						fmt.Fprintf(stdout, "%s updated successfully\n", dep)
					}

				} else {
					if err := repo.Get(); err != nil {
						fmt.Fprintf(stderr, "[%s] Can't fetch repo: %s\n", dep, err.Error())
						continue
					}

					newCount++

					if ctx.Is("verbose") {
						fmt.Fprintf(stdout, "%s fetched successfully.\n", dep)
					}
				}
			}

			if ctx.Is("verbose") {
				fmt.Fprintf(stdout, "\nDone. %d dependencie(s) created, %d dependencie(s) updated.\n", newCount, updatedCount)
			}

			return 0
		},
	}
}

func testCommand(stdout io.Writer, stderr io.Writer) climax.Command {

	return climax.Command{
		Name:  "test",
		Brief: "Parse each .scl file in a directory and compare the output to an .hcl file",
		Usage: `[options] [file-glob...]`,
		Help:  "Parse each .scl file in a directory and compare the output to an .hcl file",

		Flags: standardParserParams(),

		Handle: func(ctx climax.Context) int {

			errors := 0

			reportError := func(path string, err string, args ...interface{}) {
				fmt.Fprintf(stderr, "%-7s %s %s\n", "FAIL", path, fmt.Sprintf(err, args...))
				errors++
			}

			if len(ctx.Args) == 0 {
				fmt.Fprintf(stderr, "At least one file glob is required. See `sep help test` for syntax")
				return 1
			}

			newlineMatcher := regexp.MustCompile("\n\n")
			params, includePaths := parserParams(ctx)

			for _, fileName := range ctx.Args {

				fs := scl.NewDiskSystem()
				parser, err := scl.NewParser(fs)
				now := time.Now()

				if err != nil {
					reportError("Unable to create new parser in CWD: %s", err.Error())
					continue
				}

				for _, includeDir := range includePaths {
					parser.AddIncludePath(includeDir)
				}

				for _, p := range params {
					parser.SetParam(p.name, p.value)
				}

				if err := parser.Parse(fileName); err != nil {
					reportError(fileName, "Unable to parse file: %s", err.Error())
					continue
				}

				hclFilePath := strings.TrimSuffix(fileName, ".scl") + ".hcl"
				hclFile, _, err := fs.ReadCloser(hclFilePath)

				if err != nil {
					fmt.Fprintf(stdout, "%-7s %s [no .hcl file]\n", "?", fileName)
					continue
				}

				hcl, err := ioutil.ReadAll(hclFile)

				if err != nil {
					reportError(fileName, "Unable to read .hcl file: %s", err.Error())
					continue
				}

				hclLines := strings.Split(strings.TrimSuffix(newlineMatcher.ReplaceAllString(string(hcl), "\n"), "\n"), "\n")
				sclLines := strings.Split(parser.String(), "\n")

				diff := difflib.Diff(hclLines, sclLines)

				success := true

				for _, d := range diff {
					if d.Delta != difflib.Common {
						success = false
					}
				}

				if !success {
					reportError(fileName, "Diff failed:")

					fmt.Fprintln(stderr)

					for _, d := range diff {
						fmt.Fprintf(stderr, "\t%s\n", d.String())
					}

					fmt.Fprintln(stderr)

					continue
				}

				fmt.Fprintf(stdout, "%-7s %s\t%.3fs\n", "ok", fileName, time.Since(now).Seconds())
			}

			if errors > 0 {
				fmt.Fprintf(stderr, "\n[FAIL] %d error(s)\n", errors)
				return 1
			}

			return 0
		},
	}
}

func standardParserParams() []climax.Flag {

	return []climax.Flag{
		{
			Name:     "include",
			Short:    "i",
			Usage:    `--include /path/to/lib1,/path/to/lib2`,
			Help:     `Comma-separated list of include paths`,
			Variable: true,
		},
		{
			Name:     "param",
			Short:    "p",
			Usage:    `--param param0=somthing,"param1='something else'"`,
			Help:     `Comma-separated list of include paths`,
			Variable: true,
		},
		{
			Name:  "no-env",
			Short: "ne",
			Usage: `--no-env`,
			Help:  `Don't import envionment variables when parsing the SCL`,
		},
	}

}

func parserParams(ctx climax.Context) (params paramSlice, includePaths []string) {

	if !ctx.Is("no-env") {
		for _, envVar := range os.Environ() {
			params.Set(envVar)
		}
	}

	if ps, set := ctx.Get("param"); set {
		for _, p := range strings.Split(ps, ",") {
			params.Set(p)
		}
	}

	if ps, set := ctx.Get("include"); set {
		for _, i := range strings.Split(ps, ",") {
			includePaths = append(includePaths, i)
		}
	}

	return
}
