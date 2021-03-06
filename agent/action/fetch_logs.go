package action

import (
	"errors"

	"github.com/cloudfoundry/bosh-agent/agent/httpblobprovider/blobstore_delegator"
	boshdirs "github.com/cloudfoundry/bosh-agent/settings/directories"
	bosherr "github.com/cloudfoundry/bosh-utils/errors"
	boshcmd "github.com/cloudfoundry/bosh-utils/fileutil"
)

type FetchLogsAction struct {
	compressor  boshcmd.Compressor
	copier      boshcmd.Copier
	blobstore   blobstore_delegator.BlobstoreDelegator
	settingsDir boshdirs.Provider
}

func NewFetchLogs(
	compressor boshcmd.Compressor,
	copier boshcmd.Copier,
	blobstore blobstore_delegator.BlobstoreDelegator,
	settingsDir boshdirs.Provider,
) (action FetchLogsAction) {
	action.compressor = compressor
	action.copier = copier
	action.blobstore = blobstore
	action.settingsDir = settingsDir
	return
}

func (a FetchLogsAction) IsAsynchronous(_ ProtocolVersion) bool {
	return true
}

func (a FetchLogsAction) IsPersistent() bool {
	return false
}

func (a FetchLogsAction) IsLoggable() bool {
	return true
}

func (a FetchLogsAction) Run(logType string, filters []string) (value map[string]string, err error) {
	var logsDir string

	switch logType {
	case "job":
		if len(filters) == 0 {
			filters = []string{"**/*"}
		}
		logsDir = a.settingsDir.LogsDir()
	case "agent":
		if len(filters) == 0 {
			filters = []string{"**/*"}
		}
		logsDir = a.settingsDir.AgentLogsDir()
	default:
		err = bosherr.Error("Invalid log type")
		return
	}

	tmpDir, err := a.copier.FilteredCopyToTemp(logsDir, filters)
	if err != nil {
		err = bosherr.WrapError(err, "Copying filtered files to temp directory")
		return
	}

	defer a.copier.CleanUp(tmpDir)

	tarball, err := a.compressor.CompressFilesInDir(tmpDir)
	if err != nil {
		err = bosherr.WrapError(err, "Making logs tarball")
		return
	}

	defer func() {
		_ = a.compressor.CleanUp(tarball)
	}()

	blobID, multidigestSha, err := a.blobstore.Write("", tarball, nil)
	if err != nil {
		err = bosherr.WrapError(err, "Create file on blobstore")
		return
	}

	value = map[string]string{"blobstore_id": blobID, "sha1": multidigestSha.String()}
	return
}

func (a FetchLogsAction) Resume() (interface{}, error) {
	return nil, errors.New("not supported")
}

func (a FetchLogsAction) Cancel() error {
	return errors.New("not supported")
}
