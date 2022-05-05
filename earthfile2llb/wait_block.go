package earthfile2llb

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/earthly/earthly/states"
	"github.com/earthly/earthly/util/llbutil"
	"github.com/earthly/earthly/util/llbutil/pllb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	"github.com/moby/buildkit/frontend/gateway/client"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/pkg/errors"
)

type saveImagesWaitItem struct {
	converters []*Converter
	images     []states.SaveImage
}

type runCommandWaitItem struct {
	c   *Converter
	cmd *pllb.State
}

type waitItem interface {
	wait(context.Context) error
}

func (w *saveImagesWaitItem) wait(ctx context.Context) error {
	isMultiPlatform := make(map[string]bool)    // DockerTag -> bool
	noManifestListImgs := make(map[string]bool) // DockerTag -> bool
	platformImgNames := make(map[string]bool)

	for _, saveImage := range w.images {
		if saveImage.NoManifestList {
			noManifestListImgs[saveImage.DockerTag] = true
		} else {
			isMultiPlatform[saveImage.DockerTag] = true
		}
		if isMultiPlatform[saveImage.DockerTag] && noManifestListImgs[saveImage.DockerTag] {
			return fmt.Errorf("cannot save image %s defined multiple times, but declared as SAVE IMAGE --no-manifest-list", saveImage.DockerTag)
		}
	}

	metadata := map[string][]byte{}
	refs := map[string]client.Reference{}

	refID := 0
	for imageIndex, saveImage := range w.images {
		c := w.converters[imageIndex]
		ref, err := llbutil.StateToRef(
			ctx, c.opt.GwClient, saveImage.State, c.opt.NoCache,
			c.platr, c.opt.CacheImports.AsMap())
		if err != nil {
			return errors.Wrapf(err, "failed to solve image required for %s", saveImage.DockerTag)
		}

		config, err := json.Marshal(saveImage.Image)
		if err != nil {
			return errors.Wrapf(err, "marshal save image config")
		}

		refKey := fmt.Sprintf("image-%d", refID)
		refPrefix := fmt.Sprintf("ref/%s", refKey)
		refs[refKey] = ref

		metadata[refPrefix+"/image.name"] = []byte(saveImage.DockerTag)
		metadata[refPrefix+"/export-image-push"] = []byte("true")
		if saveImage.InsecurePush {
			metadata[refPrefix+"/insecure-push"] = []byte("true")
		}
		metadata[refPrefix+"/"+exptypes.ExporterImageConfigKey] = config
		refID++

		if isMultiPlatform[saveImage.DockerTag] {
			platformStr := saveImage.Platform.String()
			platformImgName, err := llbutil.PlatformSpecificImageName(saveImage.DockerTag, saveImage.Platform)
			if err != nil {
				return err
			}

			if saveImage.CheckDuplicate && saveImage.DockerTag != "" {
				if _, found := platformImgNames[platformImgName]; found {
					return errors.Errorf(
						"image %s is defined multiple times for the same platform (%s)",
						saveImage.DockerTag, platformImgName)
				}
				platformImgNames[platformImgName] = true
			}

			metadata[refPrefix+"/platform"] = []byte(platformStr)
		}
	}

	if len(w.converters) == 0 {
		panic("saveImagesWaitItem should never have been created with zero converters")
	}
	gatewayClient := w.converters[0].opt.GwClient // could be any converter's gwClient (they should app be the same)

	err := gatewayClient.SaveImage(ctx, gwclient.SaveImageRequest{
		Refs:     refs,
		Metadata: metadata,
	})
	if err != nil {
		return errors.Wrap(err, "failed to SAVE IMAGE")
	}
	return nil
}

func (w *runCommandWaitItem) wait(ctx context.Context) error {
	state := *w.cmd
	ref, err := llbutil.StateToRef(
		ctx, w.c.opt.GwClient, state, w.c.opt.NoCache,
		w.c.platr, w.c.opt.CacheImports.AsMap()) // FIXME c now points to the wrong converter
	if err != nil {
		return errors.Wrap(err, "waiting for RUN state to ref")
	}

	// wait for ref to be solved
	_, err = ref.ReadDir(ctx, gwclient.ReadDirRequest{Path: "/"})
	if err != nil {
		return errors.Wrap(err, "waiting for RUN command to complete")
	}
	return nil
}

type waitBlock struct {
	items []waitItem
}

func newWaitBlock() *waitBlock {
	return &waitBlock{}
}

func (wb *waitBlock) lastItem() (waitItem, bool) {
	n := len(wb.items)
	if n > 0 {
		return wb.items[n-1], true
	}
	return nil, false
}

func (wb *waitBlock) getOrCreateSaveImageWaitItem() *saveImagesWaitItem {
	var siwi *saveImagesWaitItem
	item, ok := wb.lastItem()
	if ok {
		siwi, ok = item.(*saveImagesWaitItem)
	}
	if !ok {
		siwi = &saveImagesWaitItem{}
		wb.items = append(wb.items, siwi)
	}
	return siwi
}

func (wb *waitBlock) addSaveImage(si states.SaveImage, c *Converter) {
	item := wb.getOrCreateSaveImageWaitItem()
	item.converters = append(item.converters, c)
	item.images = append(item.images, si)
}

func (wb *waitBlock) addCommand(cmd *pllb.State, c *Converter) {
	item := runCommandWaitItem{
		c:   c,
		cmd: cmd,
	}
	wb.items = append(wb.items, &item)
}

func (wb *waitBlock) wait(ctx context.Context) error {
	for _, item := range wb.items {
		err := item.wait(ctx)
		if err != nil {
			return err
		}
	}
	return nil
}
