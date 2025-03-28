/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package images

import (
	"fmt"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/cmd/ctr/commands"
	"github.com/containerd/containerd/v2/cmd/ctr/commands/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/imgcrypt/cmd/ctr/commands/flags"
	"github.com/containerd/log"
	"github.com/containerd/platforms"

	"github.com/containerd/imgcrypt/v2"
	"github.com/containerd/imgcrypt/v2/images/encryption"
	"github.com/containerd/imgcrypt/v2/images/encryption/parsehelpers"

	"github.com/opencontainers/image-spec/identity"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/urfave/cli/v2"
)

var pullCommand = cli.Command{
	Name:      "pull",
	Usage:     "pull an image from a remote",
	ArgsUsage: "[flags] <ref>",
	Description: `Fetch and prepare an image for use in containerd.

After pulling an image, it should be ready to use the same reference in a run
command. As part of this process, we do the following:

1. Fetch all resources into containerd.
2. Prepare the snapshot filesystem with the pulled resources.
3. Register metadata for the image.
`,
	Flags: append(append(append(commands.RegistryFlags, append(commands.SnapshotterFlags, commands.LabelFlag)...),
		&cli.StringSliceFlag{
			Name:  "platform",
			Usage: "Pull content from a specific platform",
			Value: &cli.StringSlice{},
		},
		&cli.BoolFlag{
			Name:  "all-platforms",
			Usage: "pull content and metadata from all platforms",
		},
		&cli.BoolFlag{
			Name:  "all-metadata",
			Usage: "Pull metadata for all platforms",
		},
		&cli.BoolFlag{
			Name:  "print-chainid",
			Usage: "Print the resulting image's chain ID",
		},
		&cli.IntFlag{
			Name:  "max-concurrent-downloads",
			Usage: "Set the max concurrent downloads for each pull",
		},
	), flags.ImageDecryptionFlags...,
	),
	Action: func(context *cli.Context) error {
		var (
			ref = context.Args().First()
		)
		if ref == "" {
			return fmt.Errorf("please provide an image reference to pull")
		}

		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()

		ctx, done, err := client.WithLease(ctx)
		if err != nil {
			return err
		}
		defer done(ctx)

		config, err := content.NewFetchConfig(ctx, context)
		if err != nil {
			return err
		}

		img, err := content.Fetch(ctx, client, ref, config)
		if err != nil {
			return err
		}

		log.G(ctx).WithField("image", ref).Debug("unpacking")

		// TODO: Show unpack status

		var p []ocispec.Platform
		if context.Bool("all-platforms") {
			p, err = images.Platforms(ctx, client.ContentStore(), img.Target)
			if err != nil {
				return fmt.Errorf("unable to resolve image platforms: %w", err)
			}
		} else {
			for _, s := range context.StringSlice("platform") {
				ps, err := platforms.Parse(s)
				if err != nil {
					return fmt.Errorf("unable to parse platform %s: %w", s, err)
				}
				p = append(p, ps)
			}
		}
		if len(p) == 0 {
			p = append(p, platforms.DefaultSpec())
		}

		cc, err := parsehelpers.CreateDecryptCryptoConfig(ParseEncArgs(context), nil)
		if err != nil {
			return err
		}
		ltdd := imgcrypt.Payload{
			DecryptConfig: *cc.DecryptConfig,
		}
		opts := encryption.WithUnpackConfigApplyOpts(encryption.WithDecryptedUnpack(&ltdd))

		start := time.Now()
		for _, platform := range p {
			fmt.Printf("unpacking %s %s...\n", platforms.Format(platform), img.Target.Digest)
			i := containerd.NewImageWithPlatform(client, img, platforms.Only(platform))
			err = i.Unpack(ctx, context.String("snapshotter"), opts)
			if err != nil {
				return err
			}
			if context.Bool("print-chainid") {
				diffIDs, err := i.RootFS(ctx)
				if err != nil {
					return err
				}
				chainID := identity.ChainID(diffIDs).String()
				fmt.Printf("image chain ID: %s\n", chainID)
			}
		}
		fmt.Printf("done: %s\t\n", time.Since(start))
		return nil
	},
}
