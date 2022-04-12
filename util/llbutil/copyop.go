package llbutil

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/earthly/earthly/util/llbutil/pllb"
	"github.com/earthly/earthly/util/platutil"
	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

// CopyOp is a simplified llb copy operation.
func CopyOp(srcState pllb.State, srcs []string, destState pllb.State, dest string, allowWildcard bool, isDir bool, keepTs bool, chown string, chmod *fs.FileMode, ifExists, symlinkNoFollow, merge bool, opts ...llb.ConstraintsOpt) pllb.State {
	destAdjusted := dest
	if dest == "." || dest == "" || len(srcs) > 1 {
		destAdjusted += string("/") // TODO: needs to be the containers platform, not the earthly hosts platform. For now, this is always Linux.
	}
	var baseCopyOpts []llb.CopyOption
	if chown != "" {
		baseCopyOpts = append(baseCopyOpts, llb.WithUser(chown))
	}
	var fa *pllb.FileAction
	if !keepTs {
		baseCopyOpts = append(baseCopyOpts, llb.WithCreatedTime(*defaultTs()))
	}
	for _, src := range srcs {
		if ifExists {
			// If the copy came in as optional (ifExists), then we need to trigger the
			// underlying wildcard matching and allow empty wildcards. The matching uses
			// the filepath.Match syntax, so by simply creating a wildcard where the
			// first letter needs to match the current first letter gets us the single
			// match; and no error if it is missing.

			//Normalize path by dropping './'
			src = strings.TrimPrefix(src, "./")
			src = fmt.Sprintf("[%s]%s", string(src[0]), string(src[1:]))
		}
		copyOpts := append([]llb.CopyOption{
			&llb.CopyInfo{
				Mode:                chmod,
				FollowSymlinks:      !symlinkNoFollow,
				CopyDirContentsOnly: !isDir,
				AttemptUnpack:       false,
				CreateDestPath:      true,
				AllowWildcard:       allowWildcard,
				AllowEmptyWildcard:  ifExists,
			},
		}, baseCopyOpts...)
		if fa == nil {
			fa = pllb.Copy(srcState, src, destAdjusted, copyOpts...)
		} else {
			fa = fa.Copy(srcState, src, destAdjusted, copyOpts...)
		}
	}
	if fa == nil {
		return destState
	}
	if merge && chown == "" {
		return pllb.Merge([]pllb.State{destState, pllb.Scratch().File(fa)}, opts...)
	}
	return destState.File(fa, opts...)
}

// Docker's internal image for running COPY.
// Ref: https://github.com/moby/buildkit/blob/v0.9.3/frontend/dockerfile/dockerfile2llb/convert.go#L40
const copyImg = "docker/dockerfile-copy:v0.1.9@sha256:e8f159d3f00786604b93c675ee2783f8dc194bb565e61ca5788f6a6e9d304061"

// CopyWithRunOptions copies from `src` to `dest` and returns the result in a separate LLB State.
// This operation is similar llb.Copy, however, it can apply llb.RunOptions (such as a mount)
// Interanally, the operation runs on the internal COPY image used by Dockerfile.
func CopyWithRunOptions(srcState pllb.State, src, dest string, platr *platutil.Resolver, opts ...llb.RunOption) pllb.State {
	// Use the native platform instead of the target platform.
	imgOpts := []llb.ImageOption{llb.MarkImageInternal, llb.Platform(platr.LLBNative())}

	// The following executes the `copy` command, which is a custom exectuable
	// contained in the Dockerfile COPY image above. The following .Run()
	// operation executes in a state constructed from that Dockerfile COPY image,
	// with the Earthly user's state mounted at /dest on that image.
	opts = append(opts, []llb.RunOption{
		llb.ReadonlyRootFS(),
		llb.Shlexf("copy %s /dest/%s", src, dest)}...)
	copyState := pllb.Image(copyImg, imgOpts...)
	run := copyState.Run(opts...)
	destState := run.AddMount("/dest", srcState)
	destState = destState.Platform(platr.ToLLBPlatform(platr.Current()))
	return destState
}

// CopyWithRunOptions copies from `src` to `dest` and returns the result in a separate LLB State.
// This operation is similar llb.Copy, however, it can apply llb.RunOptions (such as a mount)
// Interanally, the operation runs on the internal COPY image used by Dockerfile.
func CopyToCache(srcState pllb.State, path string, platr *platutil.Resolver, cacheOpt llb.RunOption) pllb.State {
	// In order to copy files into the CACHE, we first copy files into a new state,
	// then mount that state under "/0d96e302-5583-44f7-9907-6babb3d9782c" (a random uuid that was picked to avoid conflicting with any user paths),
	// and also mount the CACHE, then use the dockerfile-copy image to copy files into the cache.
	// We then force buildkit to resolve this opperation before continuing in order to warm the cache.
	imgOpts := []llb.ImageOption{llb.MarkImageInternal, llb.Platform(platr.LLBNative())}
	opts := []llb.RunOption{
		llb.ReadonlyRootFS(),
		llb.Shlexf("copy %s %s", "/0d96e302-5583-44f7-9907-6babb3d9782c/"+path, path),
		cacheOpt,
	}
	copyState := pllb.Image(copyImg, imgOpts...)
	run := copyState.Run(opts...).AddMount("/0d96e302-5583-44f7-9907-6babb3d9782c/", srcState)
	return run.Platform(platr.ToLLBPlatform(platr.Current()))
}

func FakeDepend(platr *platutil.Resolver, srcState pllb.State, extraStates ...pllb.State) pllb.State {
	opts := []llb.RunOption{
		llb.ReadonlyRootFS(),
		llb.Shlexf("true"),
	}
	for i, s := range extraStates {
		mnt := pllb.AddMount(fmt.Sprintf("/20d84d69-aea8-4905-bbd2-2c3a9f9a0ef7/%d", i), s)
		opts = append(opts, mnt)
	}
	run := srcState.Run(opts...).Root()
	return run.Platform(platr.ToLLBPlatform(platr.Current()))
}

// Abs prepends the working dir to the given path, if the
// path is relative.
func Abs(ctx context.Context, s pllb.State, p string) (string, error) {
	if path.IsAbs(p) {
		return p, nil
	}
	dir, err := s.GetDir(ctx)
	if err != nil {
		return "", errors.Wrap(err, "get dir")
	}
	return path.Join(dir, p), nil
}

var defaultTsValue time.Time
var defaultTsParse sync.Once

func defaultTs() *time.Time {
	defaultTsParse.Do(func() {
		var err error
		defaultTsValue, err = time.Parse(time.RFC3339, "2020-04-16T12:00:00+00:00")
		if err != nil {
			panic(err)
		}
	})
	return &defaultTsValue
}
