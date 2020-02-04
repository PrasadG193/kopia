package cli

import (
	"context"

	"gopkg.in/alecthomas/kingpin.v2"

	"github.com/kopia/kopia/repo/blob"
	"github.com/kopia/kopia/repo/blob/azure"
)

func init() {
	var azOptions azure.Options

	RegisterStorageConnectFlags(
		"azure",
		"an Azure blob storage",
		func(cmd *kingpin.CmdClause) {
			cmd.Flag("container", "Name of the Azure container").Required().StringVar(&azOptions.Container)
			cmd.Flag("account-name", "Azure account name(overrides AZURE_ACCOUNT_NAME environment variable)").Required().Envar("AZURE_ACCOUNT_NAME").StringVar(&azOptions.AccountName)
			cmd.Flag("account-key", "Azure account key(overrides AZURE_ACCOUNT_KEY environment variable)").Required().Envar("AZURE_ACCOUNT_KEY").StringVar(&azOptions.AccountKey)
			cmd.Flag("prefix", "Prefix to use for objects in the bucket").StringVar(&azOptions.Prefix)
			cmd.Flag("max-download-speed", "Limit the download speed.").PlaceHolder("BYTES_PER_SEC").IntVar(&azOptions.MaxDownloadSpeedBytesPerSecond)
			cmd.Flag("max-upload-speed", "Limit the upload speed.").PlaceHolder("BYTES_PER_SEC").IntVar(&azOptions.MaxUploadSpeedBytesPerSecond)
		},
		func(ctx context.Context, isNew bool) (blob.Storage, error) {
			return azure.New(ctx, &azOptions)
		},
	)
}
