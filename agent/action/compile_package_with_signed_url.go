package action

import (
	"errors"

	boshmodels "github.com/cloudfoundry/bosh-agent/agent/applier/models"
	boshcomp "github.com/cloudfoundry/bosh-agent/agent/compiler"
	boshcrypto "github.com/cloudfoundry/bosh-utils/crypto"
	bosherr "github.com/cloudfoundry/bosh-utils/errors"
)

type CompilePackageWithSignedURLRequest struct {
	PackageGetSignedURL string `json:"package_get_signed_url"`
	UploadSignedURL     string `json:"upload_signed_url"`

	Digest  boshcrypto.MultipleDigest `json:"digest"`
	Name    string                    `json:"name"`
	Version string                    `json:"version"`
	Deps    boshcomp.Dependencies     `json:"deps"`
}

type CompilePackageWithSignedURL struct {
	compiler boshcomp.Compiler
}

func NewCompilePackageWithSignedURL(compiler boshcomp.Compiler) (compilePackage CompilePackageWithSignedURL) {
	return CompilePackageWithSignedURL{
		compiler: compiler,
	}
}

func (a CompilePackageWithSignedURL) Run(request CompilePackageWithSignedURLRequest) (map[string]interface{}, error) {
	pkg := boshcomp.Package{
		Name:                request.Name,
		Sha1:                request.Digest,
		Version:             request.Version,
		PackageGetSignedURL: request.PackageGetSignedURL,
		UploadSignedURL:     request.UploadSignedURL,
	}

	modelsDeps := []boshmodels.Package{}

	for _, dep := range request.Deps {
		modelsDeps = append(modelsDeps, boshmodels.Package{
			Name:    dep.Name,
			Version: dep.Version,
			Source: boshmodels.Source{
				Sha1:        dep.Sha1,
				BlobstoreID: dep.BlobstoreID,
				SignedURL:   dep.PackageGetSignedURL,
			},
		})
	}

	_, uploadedDigest, err := a.compiler.Compile(pkg, modelsDeps)
	if err != nil {
		return map[string]interface{}{}, bosherr.WrapErrorf(err, "Compiling package %s", pkg.Name)
	}

	result := map[string]interface{}{
		"sha1": uploadedDigest.String(),
	}

	return map[string]interface{}{
		"result": result,
	}, nil
}

func (a CompilePackageWithSignedURL) Resume() (interface{}, error) {
	return nil, errors.New("not supported")
}

func (a CompilePackageWithSignedURL) Cancel() error {
	return errors.New("not supported")
}

func (a CompilePackageWithSignedURL) IsAsynchronous(_ ProtocolVersion) bool {
	return true
}

func (a CompilePackageWithSignedURL) IsPersistent() bool {
	return false
}

func (a CompilePackageWithSignedURL) IsLoggable() bool {
	return true
}
