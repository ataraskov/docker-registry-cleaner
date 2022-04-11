package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/docker/distribution/reference"
	"github.com/docker/distribution/registry/client"
	"github.com/docker/distribution/registry/client/auth"
	"github.com/docker/distribution/registry/client/auth/challenge"
	"github.com/docker/distribution/registry/client/transport"

	"golang.org/x/mod/semver"
)

type creds struct {
	username      string
	password      string
	refreshTokens map[string]string
}

func (c *creds) Basic(*url.URL) (string, string) {
	return c.username, c.password
}

func (c *creds) RefreshToken(u *url.URL, service string) string {
	return c.refreshTokens[service]
}

func (c *creds) SetRefreshToken(u *url.URL, service string, token string) {
	if c.refreshTokens != nil {
		c.refreshTokens[service] = token
	}
}

var (
	// build options
	version   = "???"
	gitCommit = "???"
	// input args
	username     = flag.String("username", "", "registry username")
	password     = flag.String("password", "", "registry password")
	registryURL  = flag.String("registry", "http://localhost:5000", "registry URL")
	repoFilter   = flag.String("repository", "", "repository name to use")
	tagFilter    = flag.String("tag", ".+", "tag regex filter")
	delete       = flag.Bool("delete", false, "delete found image(s)")
	semverSort   = flag.Bool("semver", false, "use semantic versioning sort (instead of lexicographical)")
	showVer      = flag.Bool("version", false, "show version info and exit")
	retention    = flag.Int("retention", 0, "copies to keep")
	headerAccept = "application/vnd.docker.distribution.manifest.v2+json"
)

func main() {
	flag.Parse()

	if *showVer {
		fmt.Printf("Version: %s (GIT: %s)\n", version, gitCommit)
		return
	}

	fmt.Println("# Args =>", " registry:", *registryURL, " user:", *username, " project:", *repoFilter, " tag:", *tagFilter, " retention:", *retention, " semver:", *semverSort, " delete:", *delete)
	ctx, ctxCancel := context.WithTimeout(context.Background(), time.Second*3)
	defer ctxCancel()

	creds := &creds{username: *username, password: *password}
	challengeManager := challenge.NewSimpleManager()

	fmt.Println("# Creating client ...")
	trans := transport.NewTransport(
		nil, auth.NewAuthorizer(challengeManager, auth.NewBasicHandler(creds)),
		transport.NewHeaderRequestModifier(http.Header{http.CanonicalHeaderKey("Accept"): []string{headerAccept}}))
	reg, err := client.NewRegistry(*registryURL, trans)
	if err != nil {
		log.Fatal("client: ", err)
	}

	fmt.Println("# Getting repos ...")
	names := make([]string, 100)
	num := 0
	if *repoFilter == "" {
		num, err = reg.Repositories(ctx, names, "")
		if err != nil && err != io.EOF {
			log.Fatal("repos: ", err)
		}
	} else {
		num = 1
		names[0] = *repoFilter
	}
	names = names[:num]

	for j, projName := range names {
		ref, err := reference.WithName(projName)
		if err != nil {
			log.Fatal("projects: ", err)
		}
		repo, err := client.NewRepository(ref, *registryURL, trans)
		if err != nil {
			log.Fatal("repo: ", err)
		}

		tagSrv := repo.Tags(ctx)
		tagNames, err := tagSrv.All(ctx)
		if err != nil {
			log.Fatal("tags: ", err)
		}

		// lexicographical sort first, as we rearely see true semver in the wild :)
		sort.Strings(tagNames)
		// semver sort
		if *semverSort {
			sort.SliceStable(tagNames, func(i, j int) bool {
				re := regexp.MustCompile(`(\d+\.\d+\.\d+)`)
				if semver.Compare("v"+re.FindString(tagNames[i]), "v"+re.FindString(tagNames[j])) < 0 {
					return true
				}
				return false
			})
		}

		// filter by tag
		var tags []string
		for _, name := range tagNames {
			match, err := regexp.MatchString(*tagFilter, name)
			if err != nil {
				log.Fatal("tag filter: ", err)
			}
			if !match {
				continue
			}
			tags = append(tags, name)
		}

		// info
		fmt.Printf("# %v) Project: %v, tags: %d, matching: %d\n", j+1, projName, len(tagNames), len(tags))

		// retention
		limit := len(tags)
		if *retention > -1 {
			limit = len(tags) - *retention
			if limit >= len(tags) {
				limit = len(tags)
			}
		}

		if limit <= 0 {
			fmt.Println("No images found")
			continue
		}

		for _, name := range tags[:limit] {
			match, err := regexp.MatchString(*tagFilter, name)
			if err != nil {
				log.Fatal(err)
			}
			if !match {
				continue
			}

			descr, err := tagSrv.Get(ctx, name)
			if err != nil {
				if strings.Contains(err.Error(), "manifest unknown") {
					fmt.Printf("Warning: tag '%s' was not found, skipping\n", name)
					continue
				}
				log.Fatal("tag service: ", err)
			}
			fmt.Println(projName+":"+name, " size:", descr.Size, " digets:", descr.Digest, " delete:", *delete)

			// delete
			if *delete {
				manifestSrv, err := repo.Manifests(ctx)
				if err != nil {
					log.Fatal("manifest service: ", err)
				}
				exists, err := manifestSrv.Exists(ctx, descr.Digest)
				if err != nil {
					log.Fatal("exists: ", err)
				}
				if exists {
					err := manifestSrv.Delete(ctx, descr.Digest)
					if err != nil {
						log.Fatal("delete: ", err)
					}
				}
			}
		}

	}
}
