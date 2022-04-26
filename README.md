[![Image](https://github.com/ataraskov/docker-registry-cleaner/actions/workflows/image.yml/badge.svg)](https://github.com/ataraskov/docker-registry-cleaner/actions/workflows/image.yml) [![Release](https://github.com/ataraskov/docker-registry-cleaner/actions/workflows/release.yml/badge.svg)](https://github.com/ataraskov/docker-registry-cleaner/actions/workflows/release.yml)

# docker-registry-cleaner
Simple and ugly docker registry cleaner

## Basic usage

By default nothing is removed, just printed.

This project is just marking tags for removal. Docker registry will not remove actual data from disk until garbage-collect is ran against it.

Be cautions with `--retention` option as it applied to list of tags. Many-to-one relationship between tags and images can lead to data loss, when used with `--retention` option.

Using binary:

    docker-registry-cleaner --registry http://registry:5000

Using docker image:

    docker run -it --rm --network srv_default ghcr.io/ataraskov/docker-registry-cleaner:0.0.3 --registry http://registry:5000 

Help/Options:

    --registry      registry URL (default "http://localhost:5000")
    --username      registry username
    --password      registry password
    --repository    repository name to use
    --tag           tag regex filter (default ".+")
    --days          days old filter
    --retention     copies to keep (applies to tags list)
    --semver        use semantic versioning sort (instead of lexicographical)
    --delete        delete found image(s)
    --version       show version info and exit


## Examples

List images sorted in semantic versioning fashion for registry 'registry:5000' in 'my-net' docker network:

    docker run --rm --network my-net ghcr.io/ataraskov/docker-registry-cleaner:0.0.2 --semver --registry http://registry:5000

Mark to remove tags starting with 'dev' from repository 'my-repo' older than 7 days (image will not be removed if it has at least one other tag left, i.e. not starting with 'dev'):

    docker-registry-cleaner --repository my-repo --tag '^dev' --days 7 --delete

Run garbage collect on registry running in container 'my-registry' (this command removes blobs from the disk):

    docker exec my-registry bin/registry garbage-collect /etc/docker/registry/config.yml