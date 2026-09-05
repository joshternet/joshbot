package main

import (
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/joshternet/joshbot/internal/githubpublish"
)

const (
	publishOwnerEnvironment = "JOSHBOT_PUBLISH_GITHUB_OWNER"

	publishRepositoryEnvironment = "JOSHBOT_PUBLISH_GITHUB_REPOSITORY"

	publishBranchEnvironment = "JOSHBOT_PUBLISH_GITHUB_BRANCH"

	githubTokenFileEnvironment = "JOSHBOT_GITHUB_TOKEN_FILE"

	defaultPublishBranch = "main"
	maxGitHubTokenSize   = 4_096
)

var (
	errInvalidPublishConfiguration = errors.New(
		"publication configuration is invalid",
	)

	errOpenGitHubTokenFile = errors.New(
		"cannot open GitHub token file",
	)

	errReadGitHubTokenFile = errors.New(
		"cannot read GitHub token file",
	)

	errEmptyGitHubToken = errors.New(
		"GitHub token file is empty",
	)

	errGitHubTokenTooLarge = errors.New(
		"GitHub token file is too large",
	)

	errInvalidGitHubToken = errors.New(
		"GitHub token file is invalid",
	)
)

type publishSettings struct {
	target githubpublish.Config
	token  string
}

func loadPublishSettings(
	getenv environmentGetter,
) (publishSettings, error) {
	owner := getenv(publishOwnerEnvironment)
	repository := getenv(
		publishRepositoryEnvironment,
	)
	branch := getenv(publishBranchEnvironment)
	tokenFile := getenv(
		githubTokenFileEnvironment,
	)

	if owner == "" ||
		repository == "" ||
		tokenFile == "" {
		return publishSettings{},
			errInvalidPublishConfiguration
	}

	if branch == "" {
		branch = defaultPublishBranch
	}

	token, err := readGitHubTokenFile(
		tokenFile,
	)
	if err != nil {
		return publishSettings{}, err
	}

	return publishSettings{
		target: githubpublish.Config{
			Owner:      owner,
			Repository: repository,
			Branch:     branch,
		},
		token: token,
	}, nil
}

func readGitHubTokenFile(
	path string,
) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", errOpenGitHubTokenFile
	}
	defer file.Close()

	return readGitHubToken(file)
}

func readGitHubToken(
	reader io.Reader,
) (string, error) {
	data, err := io.ReadAll(
		io.LimitReader(
			reader,
			int64(maxGitHubTokenSize)+2,
		),
	)
	if err != nil {
		return "", errReadGitHubTokenFile
	}

	if len(data) > 0 &&
		data[len(data)-1] == '\n' {
		data = data[:len(data)-1]
	}

	if len(data) == 0 {
		return "", errEmptyGitHubToken
	}

	if len(data) > maxGitHubTokenSize {
		return "", errGitHubTokenTooLarge
	}

	token := string(data)
	if !validGitHubTokenFileValue(token) {
		return "", errInvalidGitHubToken
	}

	return token, nil
}

func validGitHubTokenFileValue(
	token string,
) bool {
	if !utf8.ValidString(token) ||
		strings.TrimSpace(token) != token {
		return false
	}

	for _, character := range token {
		if character < 0x20 ||
			character == 0x7f {
			return false
		}
	}

	return true
}
