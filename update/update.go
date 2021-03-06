package update

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/CircleCI-Public/circleci-cli/settings"
	"github.com/blang/semver"
	"github.com/google/go-github/github"
	"github.com/pkg/errors"
	"github.com/rhysd/go-github-selfupdate/selfupdate"
)

// hoursBeforeCheck is used to configure the delay between auto-update checks
var hoursBeforeCheck = 28

// ShouldCheckForUpdates tell us if the last update check was more than a day ago
func ShouldCheckForUpdates(upd *settings.UpdateCheck) bool {
	diff := time.Since(upd.LastUpdateCheck)
	return diff.Hours() >= float64(hoursBeforeCheck)
}

// CheckForUpdates will check for updates given the proper package manager
func CheckForUpdates(githubAPI, slug, current, packageManager string) (*Options, error) {
	var (
		err   error
		check *Options
	)

	check = &Options{
		Current:        semver.MustParse(current),
		PackageManager: packageManager,

		githubAPI: githubAPI,
		slug:      slug,
	}

	switch check.PackageManager {
	case "release":
		err = checkFromSource(check)
	case "source":
		err = checkFromSource(check)
	case "homebrew":
		err = checkFromHomebrew(check)
	}

	return check, err
}

func checkFromSource(check *Options) error {
	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		EnterpriseBaseURL: check.githubAPI,
	})
	if err != nil {
		return err
	}

	check.updater = updater

	err = latestRelease(check)

	return err
}

func checkFromHomebrew(check *Options) error {
	brew, err := exec.LookPath("brew")
	if err != nil {
		return errors.Wrap(err, "Expected to find `brew` in your $PATH but wasn't able to find it")
	}

	command := exec.Command(brew, "outdated", "--json=v1") // #nosec
	out, err := command.Output()
	if err != nil {
		return errors.Wrap(err, "failed to check for updates via `brew`")
	}

	var outdated HomebrewOutdated

	err = json.Unmarshal(out, &outdated)
	if err != nil {
		return errors.Wrap(err, "failed to parse output of `brew outdated --json=v1`")
	}

	for _, o := range outdated {
		if o.Name == "circleci" {
			if len(o.InstalledVersions) > 0 {
				check.Current = semver.MustParse(o.InstalledVersions[0])
			}

			check.Latest = &selfupdate.Release{
				Version: semver.MustParse(o.CurrentVersion),
			}

			// We found a release so update state of updates check
			check.Found = true
		}
	}

	return nil
}

// HomebrewOutdated wraps the JSON output from running `brew outdated --json=v1`
// We're specifically looking for this kind of structured data from the command:
//
//   [
//     {
//       "name": "circleci",
//       "installed_versions": [
//         "0.1.1248"
//       ],
//       "current_version": "0.1.3923",
//       "pinned": false,
//       "pinned_version": null
//     },
//   ]
type HomebrewOutdated []struct {
	Name              string   `json:"name"`
	InstalledVersions []string `json:"installed_versions"`
	CurrentVersion    string   `json:"current_version"`
	Pinned            bool     `json:"pinned"`
	PinnedVersion     string   `json:"pinned_version"`
}

// Options contains everything we need to check for or perform updates of the CLI.
type Options struct {
	Current        semver.Version
	Found          bool
	Latest         *selfupdate.Release
	PackageManager string

	updater   *selfupdate.Updater
	githubAPI string
	slug      string
}

// latestRelease will set the last known release as a member on the Options instance.
// We also update options if any releases were found or not.
func latestRelease(opts *Options) error {
	latest, found, err := opts.updater.DetectLatest(opts.slug)
	opts.Latest = latest
	opts.Found = found

	if err != nil {
		if errResponse, ok := err.(*github.ErrorResponse); ok && errResponse.Response.StatusCode == http.StatusUnauthorized {
			return errors.Wrap(err, "Your Github token is invalid. Check the [github] section in ~/.gitconfig\n")
		}

		return errors.Wrap(err, "error finding latest release")
	}

	return nil
}

// IsLatestVersion will tell us if the current version is the latest version available
func IsLatestVersion(opts *Options) bool {
	if opts.Current.String() == "" || opts.Latest == nil {
		return true
	}

	return opts.Latest.Version.Equals(opts.Current)
}

// InstallLatest will execute the updater and replace the current CLI with the latest version available.
func InstallLatest(opts *Options) (string, error) {
	release, err := opts.updater.UpdateSelf(opts.Current, opts.slug)
	if err != nil {
		return "", errors.Wrap(err, "failed to install update")
	}

	return fmt.Sprintf("Updated to %s", release.Version), nil
}

// DebugVersion returns a nicely formatted string representing the state of the current version.
// Intended to be printed to standard error for developers.
func DebugVersion(opts *Options) string {
	return strings.Join([]string{
		fmt.Sprintf("Latest version: %s", opts.Latest.Version),
		fmt.Sprintf("Published: %s", opts.Latest.PublishedAt),
		fmt.Sprintf("Current Version: %s", opts.Current),
	}, "\n")
}

// ReportVersion returns a nicely formatted string representing the state of the current version.
// Intended to be printed to the user.
func ReportVersion(opts *Options) string {
	return strings.Join([]string{
		fmt.Sprintf("You are running %s", opts.Current),
		fmt.Sprintf("A new release is available (%s)", opts.Latest.Version),
	}, "\n")
}

// HowToUpdate returns a message teaching the user how to update to the latest version.
func HowToUpdate(opts *Options) string {
	switch opts.PackageManager {
	case "homebrew":
		return "You can update with `brew upgrade circleci`"
	case "release":
		return "You can update with `circleci update install`"
	case "source":
		return strings.Join([]string{
			"You can visit the Github releases page for the CLI to manually download and install:",
			"https://github.com/CircleCI-Public/circleci-cli/releases",
		}, "\n")
	}

	// Do nothing if we don't expect one of the supported package managers above
	return ""
}
