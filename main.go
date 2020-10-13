// Copyright 2020 Google LLC, Paul Durivage <durivage@google.com>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	cloudbuild "cloud.google.com/go/cloudbuild/apiv1"
	"context"
	"errors"
	"fmt"
	"github.com/spf13/pflag"
	cloudbuildpb "google.golang.org/genproto/googleapis/devtools/cloudbuild/v1"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

var (
	timeoutSigStr string
	timeoutStr    string
	timeoutDur    time.Duration
	projectId     string
	buildId       string
	cmdName       string
	cmdArgs       []string
	InfoLogger    *log.Logger
	ErrorLogger   *log.Logger
	validSignals  = map[string]os.Signal{
		"SIGABRT":   syscall.SIGABRT,
		"SIGALRM":   syscall.SIGALRM,
		"SIGBUS":    syscall.SIGBUS,
		"SIGCHLD":   syscall.SIGCHLD,
		"SIGCONT":   syscall.SIGCONT,
		"SIGFPE":    syscall.SIGFPE,
		"SIGHUP":    syscall.SIGHUP,
		"SIGILL":    syscall.SIGILL,
		"SIGINT":    syscall.SIGINT,
		"SIGIO":     syscall.SIGIO,
		"SIGIOT":    syscall.SIGIOT,
		"SIGKILL":   syscall.SIGKILL,
		"SIGPIPE":   syscall.SIGPIPE,
		"SIGPROF":   syscall.SIGPROF,
		"SIGQUIT":   syscall.SIGQUIT,
		"SIGSEGV":   syscall.SIGSEGV,
		"SIGSTOP":   syscall.SIGSTOP,
		"SIGSYS":    syscall.SIGSYS,
		"SIGTERM":   syscall.SIGTERM,
		"SIGTRAP":   syscall.SIGTRAP,
		"SIGTSTP":   syscall.SIGTSTP,
		"SIGTTIN":   syscall.SIGTTIN,
		"SIGTTOU":   syscall.SIGTTOU,
		"SIGURG":    syscall.SIGURG,
		"SIGUSR1":   syscall.SIGUSR1,
		"SIGUSR2":   syscall.SIGUSR2,
		"SIGVTALRM": syscall.SIGVTALRM,
		"SIGWINCH":  syscall.SIGWINCH,
		"SIGXCPU":   syscall.SIGXCPU,
		"SIGXFSZ":   syscall.SIGXFSZ,
	}
)

type UserRequestedHelp struct {}

func (e *UserRequestedHelp) Error() string {
	return "user requested help"
}

func runCommand(cmdName string, cmdArgs []string, timeout time.Duration, sigChan chan os.Signal) error {
	cmd := exec.Command(cmdName, cmdArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	done := make(chan error, 1)

	go func() {
		InfoLogger.Printf("Running command: %v %v", cmdName, strings.Join(cmdArgs, " "))
		done <- cmd.Run()
	}()

	var err error

	select {
	case err := <-done:
		return err
	case recdSig := <-sigChan:
		InfoLogger.Printf("Parent process received signal %v; forwarding to child command process\n", recdSig.String())
		err = cmd.Process.Signal(recdSig)
	case <-time.After(timeout):
		InfoLogger.Printf("Timeout has been reached; sending %v signal to process", timeoutSigStr)
		err = cmd.Process.Signal(validSignals[timeoutSigStr])
	}

	InfoLogger.Printf("Waiting on process to exit...")
	<-done
	return err
}

func getBuildSignalTime(ctx context.Context) (*time.Time, error) {
	InfoLogger.Println("Getting build info from Cloud Build API")

	c, err := cloudbuild.NewClient(ctx)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("Error creating Cloud Build client: %v", err.Error()))
	}

	req := &cloudbuildpb.GetBuildRequest{
		ProjectId: projectId,
		Id:        buildId,
	}

	resp, err := c.GetBuild(ctx, req)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("error getting build from API; check project and build ID: %v; ", err.Error()))
	}

	buildTimeoutTime := resp.StartTime.Seconds + resp.Timeout.Seconds
	signalTime := time.Unix(buildTimeoutTime-int64(timeoutDur.Seconds()), 0)

	if signalTime.Before(time.Now()) {
		return nil, errors.New(fmt.Sprintf("invalid signal time '%v' for build ID '%v': occurs in the past", signalTime, buildId[:8]))
	}

	InfoLogger.Printf("Cloud Build timeout is %v seconds\n", resp.Timeout.Seconds)
	InfoLogger.Printf("Cloud Build container will be terminated at %v\n", time.Unix(buildTimeoutTime, 0))
	InfoLogger.Printf("Process will be signaled at %v\n", signalTime)

	return &signalTime, nil
}

func parseArgs() (int, error) {
	pflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s: [flags ...] PROJECT_ID BUILD_ID -- COMMAND [command-flags ...]\n", os.Args[0])
		pflag.CommandLine.PrintDefaults()
	}

	pflag.StringVar(&timeoutSigStr, "signal", "SIGTERM", "signal to send to wrapped process")
	pflag.StringVar(&timeoutStr, "before-timeout", "60s", "time before build timeout to send designated signal ex: 30s, 5m")
	help := pflag.Bool("help", false, "print this usage and exit")

	pflag.Parse()

	if *help {
		return 0, &UserRequestedHelp{}
	}

	if len(pflag.Args()) < 3 {
		return 1, errors.New(fmt.Sprintf("%v requires at least 3 positional arguments, got %v", os.Args[0], len(pflag.Args())))
	}

	if _, ok := validSignals[timeoutSigStr]; !ok {
		return 1, errors.New(fmt.Sprintf("%v is not a valid, catchable signal", timeoutSigStr))
	}

	dur, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return 1, errors.New(fmt.Sprintf("error with supplied value to --before-timeout: %v", err.Error()))
	}
	timeoutDur = dur

	projectId = pflag.Arg(0)
	buildId = pflag.Arg(1)
	cmdName = pflag.Arg(2)
	cmdArgs = pflag.Args()[3:]

	return 0, nil
}

func main() {
	InfoLogger = log.New(os.Stdout, "INFO: ", log.LstdFlags)
	ErrorLogger = log.New(os.Stderr, "ERROR: ", log.LstdFlags)

	if exitCode, err := parseArgs(); err != nil {
		pflag.Usage()

		if _, ok := err.(*UserRequestedHelp); !ok {
			_, _ = fmt.Fprintf(os.Stderr, "%v\n", err.Error())
		}

		os.Exit(exitCode)
	}

	ctx := context.Background()
	signalTime, err := getBuildSignalTime(ctx)
	if err != nil {
		ErrorLogger.Fatalln(err.Error())
	}
	adjustedTimeout := signalTime.Sub(time.Now())

	caughtSigsChan := make(chan os.Signal)
	signal.Notify(caughtSigsChan)
	// catch everything but SIGCHLD
	// because we will have a child process this doesn't make sense to catch
	signal.Reset(syscall.SIGCHLD)

	if err := runCommand(cmdName, cmdArgs, adjustedTimeout, caughtSigsChan); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode := exitError.ExitCode()
			ErrorLogger.Printf("Process exited with non-zero exit code: %d\n", exitCode)
			os.Exit(exitCode)
		} else {
			ErrorLogger.Fatalln(err.Error())
		}
	} else {
		InfoLogger.Println("Process exited successfully")
	}
}
