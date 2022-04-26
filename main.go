package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/docker/distribution/manifest/schema2"
	"github.com/docker/distribution/reference"
	"github.com/docker/distribution/registry/client"
	"github.com/docker/distribution/registry/client/auth"
	"github.com/docker/distribution/registry/client/auth/challenge"
	"github.com/docker/distribution/registry/client/transport"

	"golang.org/x/mod/semver"
)

type imageConfig struct {
	Created time.Time `json:"created"`
}

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
	username    = flag.String("username", "", "registry username")
	password    = flag.String("password", "", "registry password")
	registryURL = flag.String("registry", "http://localhost:5000", "registry URL")
	repoFilter  = flag.String("repository", "", "repository name to use")
	tagFilter   = flag.String("tag", ".+", "tag regex filter")
	daysFilter  = flag.Int("days", 0, "days old filter")
	delete      = flag.Bool("delete", false, "delete found image(s)")
	semverSort  = flag.Bool("semver", false, "use semantic versioning sort (instead of lexicographical)")
	showVer     = flag.Bool("version", false, "show version info and exit")
	retention   = flag.Int("retention", 0, "copies to keep (applies to tags list)")
	headers     = http.Header{http.CanonicalHeaderKey("Accept"): {"application/vnd.docker.distribution.manifest.v2+json", "application/vnd.docker.distribution.manifest.list.v2+json"}}
)

func main() {
	flag.Parse()

	if *showVer {
		fmt.Printf("Version: %s (GIT: %s)\n", version, gitCommit)
		return
	}

	fmt.Println("# Args =>", " registry:", *registryURL, " user:", *username, " project:", *repoFilter, " tag:", *tagFilter, " retention:", *retention, " days:", *daysFilter, " semver:", *semverSort, " delete:", *delete)
	ctx, ctxCancel := context.WithTimeout(context.Background(), time.Second*3)
	defer ctxCancel()

	creds := &creds{username: *username, password: *password}
	challengeManager := challenge.NewSimpleManager()

	fmt.Println("# Creating client ...")
	trans := transport.NewTransport(
		nil, auth.NewAuthorizer(challengeManager, auth.NewBasicHandler(creds)),
		transport.NewHeaderRequestModifier(headers))
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

	for j, repoName := range names {
		ref, err := reference.WithName(repoName)
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
				re := regexp.MustCompile(`(\d+\.\d+\.\d+(-\d+)?)`)
				return semver.Compare("v"+re.FindString(tagNames[i]), "v"+re.FindString(tagNames[j])) < 0
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
		fmt.Printf("# %v) Repository: %v, tags total: %d, after tag filter: %d\n", j+1, repoName, len(tagNames), len(tags))

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

		twriter := tabwriter.NewWriter(os.Stdout, 5, 1, 2, ' ', 0)
		theader := true
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

			manifestSrv, err := repo.Manifests(ctx)
			if err != nil {
				log.Fatal("manifest service: ", err)
			}

			// filter by days old
			manifest, err := manifestSrv.Get(ctx, descr.Digest)
			if err != nil {
				log.Fatal("manifest get: ", err)
			}
			t, _, err := manifest.Payload()
			if err != nil {
				log.Fatal("manifest payload: ", err)
			}
			var image imageConfig
			if t == "application/vnd.docker.distribution.manifest.v2+json" {
				c := manifest.(*schema2.DeserializedManifest).Config

				blobSrv := repo.Blobs(ctx)
				js, err := blobSrv.Get(ctx, c.Digest)
				if err != nil {
					log.Fatal("blob get: ", err)
				}

				if err := json.Unmarshal(js, &image); err != nil {
					log.Fatal("config unmarshal:", err)
				}
			}
			if *daysFilter > 0 {
				if image.Created.After(time.Now().AddDate(0, 0, -*daysFilter)) {
					continue
				}
			}

			// print
			if theader {
				fmt.Fprintf(twriter, "%s\t%s\t%s\t%s\n", "tag", "created", "digets", "delete flag")
				fmt.Fprintf(twriter, "%s\t%s\t%s\t%s\n", "-----", "-----", "-----", "-----")
				theader = false
			}
			fmt.Fprintf(twriter, "%s\t%s\t%s\t%s\n", repoName+":"+name, image.Created.Format("2006-01-02 15:04:05 MST"), descr.Digest, strconv.FormatBool(*delete))

			// delete
			if *delete {
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
		twriter.Flush()

	}
}
