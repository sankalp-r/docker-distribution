package memory

import (
	"context"
	"math"

	"github.com/docker/distribution"
	"github.com/docker/distribution/reference"
	"github.com/docker/distribution/registry/storage/cache"
	lru "github.com/hashicorp/golang-lru"
	"github.com/opencontainers/go-digest"
)

const (
	// DefaultSize is the default cache size to use if no size is explicitly
	// configured.
	DefaultSize = 10000

	// UnlimitedSize indicates the cache size should not be limited.
	UnlimitedSize = math.MaxInt
)

type descriptorCacheKey struct {
	digest digest.Digest
	repo   string
}

type inMemoryBlobDescriptorCacheProvider struct {
	lru *lru.ARCCache
}

// NewInMemoryBlobDescriptorCacheProvider returns a new mapped-based cache for
// storing blob descriptor data.
func NewInMemoryBlobDescriptorCacheProvider(size int) cache.BlobDescriptorCacheProvider {
	if size <= 0 {
		size = math.MaxInt
	}
	lruCache, err := lru.NewARC(size)
	if err != nil {
		// NewARC can only fail if size is <= 0, so this unreachable
		panic(err)
	}
	return &inMemoryBlobDescriptorCacheProvider{
		lru: lruCache,
	}
}

func (imbdcp *inMemoryBlobDescriptorCacheProvider) RepositoryScoped(repo string) (distribution.BlobDescriptorService, error) {
	if _, err := reference.ParseNormalizedNamed(repo); err != nil {
		return nil, err
	}

	return &repositoryScopedInMemoryBlobDescriptorCache{
		repo:   repo,
		parent: imbdcp,
	}, nil
}

func (imbdcp *inMemoryBlobDescriptorCacheProvider) Stat(ctx context.Context, dgst digest.Digest) (distribution.Descriptor, error) {
	if err := dgst.Validate(); err != nil {
		return distribution.Descriptor{}, err
	}

	key := descriptorCacheKey{
		digest: dgst,
	}
	descriptor, ok := imbdcp.lru.Get(key)
	if ok {
		// Type assertion not really necessary, but included in case
		// it's necessary for the fuzzer
		if desc, ok := descriptor.(distribution.Descriptor); ok {
			return desc, nil
		}
	}
	return distribution.Descriptor{}, distribution.ErrBlobUnknown
}

func (imbdcp *inMemoryBlobDescriptorCacheProvider) Clear(ctx context.Context, dgst digest.Digest) error {
	key := descriptorCacheKey{
		digest: dgst,
	}
	imbdcp.lru.Remove(key)
	return nil
}

func (imbdcp *inMemoryBlobDescriptorCacheProvider) SetDescriptor(ctx context.Context, dgst digest.Digest, desc distribution.Descriptor) error {
	_, err := imbdcp.Stat(ctx, dgst)
	if err == distribution.ErrBlobUnknown {
		if dgst.Algorithm() != desc.Digest.Algorithm() && dgst != desc.Digest {
			// if the digests differ, set the other canonical mapping
			if err := imbdcp.SetDescriptor(ctx, desc.Digest, desc); err != nil {
				return err
			}
		}

		if err := dgst.Validate(); err != nil {
			return err
		}

		if err := cache.ValidateDescriptor(desc); err != nil {
			return err
		}

		key := descriptorCacheKey{
			digest: dgst,
		}
		imbdcp.lru.Add(key, desc)
		return nil
	}
	// we already know it, do nothing
	return err
}

// repositoryScopedInMemoryBlobDescriptorCache provides the request scoped
// repository cache. Instances are not thread-safe but the delegated
// operations are.
type repositoryScopedInMemoryBlobDescriptorCache struct {
	repo   string
	parent *inMemoryBlobDescriptorCacheProvider // allows lazy allocation of repo's map
}

func (rsimbdcp *repositoryScopedInMemoryBlobDescriptorCache) Stat(ctx context.Context, dgst digest.Digest) (distribution.Descriptor, error) {
	if err := dgst.Validate(); err != nil {
		return distribution.Descriptor{}, err
	}

	key := descriptorCacheKey{
		digest: dgst,
		repo:   rsimbdcp.repo,
	}
	descriptor, ok := rsimbdcp.parent.lru.Get(key)
	if ok {
		// Type assertion not really necessary, but included in case
		// it's necessary for the fuzzer
		if desc, ok := descriptor.(distribution.Descriptor); ok {
			return desc, nil
		}
	}
	return distribution.Descriptor{}, distribution.ErrBlobUnknown
}

func (rsimbdcp *repositoryScopedInMemoryBlobDescriptorCache) Clear(ctx context.Context, dgst digest.Digest) error {
	key := descriptorCacheKey{
		digest: dgst,
		repo:   rsimbdcp.repo,
	}
	rsimbdcp.parent.lru.Remove(key)
	return nil
}

func (rsimbdcp *repositoryScopedInMemoryBlobDescriptorCache) SetDescriptor(ctx context.Context, dgst digest.Digest, desc distribution.Descriptor) error {
	if err := dgst.Validate(); err != nil {
		return err
	}

	if err := cache.ValidateDescriptor(desc); err != nil {
		return err
	}

	key := descriptorCacheKey{
		digest: dgst,
		repo:   rsimbdcp.repo,
	}
	rsimbdcp.parent.lru.Add(key, desc)
	return rsimbdcp.parent.SetDescriptor(ctx, dgst, desc)
}
