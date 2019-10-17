package integration_test

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/cloudfoundry/bosh-agent/agent/action"
	boshcomp "github.com/cloudfoundry/bosh-agent/agent/compiler"
	"github.com/cloudfoundry/bosh-agent/agentclient"
	"github.com/cloudfoundry/bosh-agent/integration/integrationagentclient"
	"github.com/cloudfoundry/bosh-agent/settings"
	boshcrypto "github.com/cloudfoundry/bosh-utils/crypto"
)

var _ = Describe("compile_package", func() {
	var (
		agentClient      *integrationagentclient.IntegrationAgentClient
		registrySettings settings.Settings
	)

	BeforeEach(func() {
		err := testEnvironment.StopAgent()
		Expect(err).ToNot(HaveOccurred())

		err = testEnvironment.CleanupDataDir()
		Expect(err).ToNot(HaveOccurred())

		err = testEnvironment.CleanupLogFile()
		Expect(err).ToNot(HaveOccurred())

		err = testEnvironment.SetupConfigDrive()
		Expect(err).ToNot(HaveOccurred())

		err = testEnvironment.UpdateAgentConfig("config-drive-agent.json")
		Expect(err).ToNot(HaveOccurred())

		registrySettings = settings.Settings{
			AgentID: "fake-agent-id",

			// note that this SETS the username and password for HTTP message bus access
			Mbus: "https://mbus-user:mbus-pass@127.0.0.1:6868",

			Blobstore: settings.Blobstore{
				Type: "local",
				Options: map[string]interface{}{
					// this path should get rewritten internally to /var/vcap/data/blobs
					"blobstore_path": "/var/vcap/micro_bosh/data/cache",
				},
			},

			Disks: settings.Disks{
				Ephemeral: "/dev/sdh",
			},
		}

		err = testEnvironment.AttachDevice("/dev/sdh", 128, 2)
		Expect(err).ToNot(HaveOccurred())

		err = testEnvironment.StartRegistry(registrySettings)
		Expect(err).ToNot(HaveOccurred())
	})

	JustBeforeEach(func() {
		err := testEnvironment.StartAgent()
		Expect(err).ToNot(HaveOccurred())

		agentClient, err = testEnvironment.StartAgentTunnel("mbus-user", "mbus-pass", 6868)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		err := testEnvironment.StopAgentTunnel()
		Expect(err).NotTo(HaveOccurred())

		err = testEnvironment.StopAgent()
		Expect(err).NotTo(HaveOccurred())

		err = testEnvironment.DetachDevice("/dev/sdh")
		Expect(err).ToNot(HaveOccurred())
	})

	Context("when configured with a signed URL", func() {
		var (
			dummyPackageSignedURL      string
			compiledDummyPackagePutURL string
			s3Bucket                   string
		)

		multiDigest := createSHA1MultiDigest("236cbd31a483c3594061b00a84a80c1c182b3b20")

		AfterEach(func() {
			removeS3Object(s3Bucket, "dummy-package.tgz")
		})

		BeforeEach(func() {
			s3Bucket = os.Getenv("AWS_BUCKET")
			dummyReader, err := os.Open(filepath.Join("assets", "dummy_package.tgz"))
			defer dummyReader.Close()
			Expect(err).NotTo(HaveOccurred())
			uploadS3Object(s3Bucket, "dummy_package.tgz", dummyReader)
			dummyPackageSignedURL = generateSignedURLForGet(s3Bucket, "dummy_package.tgz")
			compiledDummyPackagePutURL = generateSignedURLForPut(s3Bucket, "compiled_dummy_package.tgz")
		})

		It("compiles and stores it to the blobstore", func() {
			result, err := agentClient.CompilePackageWithSignedURL(action.CompilePackageWithSignedURLRequest{
				PackageGetSignedURL: dummyPackageSignedURL,
				UploadSignedURL:     compiledDummyPackagePutURL,

				Digest:  multiDigest,
				Name:    "fake",
				Version: "1",
				Deps:    boshcomp.Dependencies{},
			})

			Expect(err).NotTo(HaveOccurred())

			downloadedContents, err := ioutil.TempFile("", "compile-package-test")
			Expect(err).ToNot(HaveOccurred())
			defer os.Remove(downloadedContents.Name())

			contents, sha1 := downloadS3ObjectContents(s3Bucket, "compiled_dummy_package.tgz")
			_, err = downloadedContents.Write(contents)
			Expect(err).NotTo(HaveOccurred())
			Expect(result["result"]).To(Equal(map[string]interface{}{"sha1": sha1}))

			s := exec.Command("stat", downloadedContents.Name())
			output, err := s.CombinedOutput()
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(MatchRegexp("regular file"))
		})

		It("allows passing bare sha1 for legacy support", func() {
			response, err := agentClient.CompilePackageWithSignedURL(action.CompilePackageWithSignedURLRequest{
				Name:                "fake",
				Version:             "1",
				PackageGetSignedURL: dummyPackageSignedURL,
				UploadSignedURL:     compiledDummyPackagePutURL,
				Digest:              multiDigest,
				Deps:                boshcomp.Dependencies{},
			})

			Expect(err).NotTo(HaveOccurred())

			downloadedContents, err := ioutil.TempFile("", "compile-package-test")
			Expect(err).ToNot(HaveOccurred())
			defer os.Remove(downloadedContents.Name())

			contents, sha1 := downloadS3ObjectContents(s3Bucket, "compiled_dummy_package.tgz")
			_, err = downloadedContents.Write(contents)
			Expect(err).NotTo(HaveOccurred())
			Expect(response["result"]).To(Equal(map[string]interface{}{"sha1": sha1}))

			s := exec.Command("zgrep", "dummy contents of dummy package file", downloadedContents.Name())
			Expect(s.Run()).NotTo(HaveOccurred())
		})

		It("does not skip verification when digest argument is missing", func() {
			_, err := agentClient.CompilePackageWithSignedURL(action.CompilePackageWithSignedURLRequest{
				Name:                "fake",
				Version:             "1",
				PackageGetSignedURL: dummyPackageSignedURL,
				UploadSignedURL:     compiledDummyPackagePutURL,
				Digest:              createSHA1MultiDigest(""),
			})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("No digest algorithm found. Supported algorithms: sha1, sha256, sha512"))
		})

		It("compiles dependencies and stores them to the blobstore", func() {
			response, err := agentClient.CompilePackageWithSignedURL(action.CompilePackageWithSignedURLRequest{
				PackageGetSignedURL: dummyPackageSignedURL,
				UploadSignedURL:     compiledDummyPackagePutURL,
				Digest:              multiDigest,
				Name:                "fake",
				Version:             "1",
				Deps: boshcomp.Dependencies{"fake-dep-1": boshcomp.Package{
					Name:                "fake-dep-1",
					PackageGetSignedURL: dummyPackageSignedURL,
					UploadSignedURL:     compiledDummyPackagePutURL,
					Sha1:                multiDigest,
					Version:             "1",
				}}})

			Expect(err).NotTo(HaveOccurred())

			downloadedContents, err := ioutil.TempFile("", "compile-package-test")
			Expect(err).ToNot(HaveOccurred())
			defer os.Remove(downloadedContents.Name())

			contents, sha1 := downloadS3ObjectContents(s3Bucket, "compiled_dummy_package.tgz")
			_, err = downloadedContents.Write(contents)
			Expect(err).NotTo(HaveOccurred())
			Expect(response["result"]).To(Equal(map[string]interface{}{"sha1": sha1}))

			s := exec.Command("stat", downloadedContents.Name())
			output, err := s.CombinedOutput()
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(MatchRegexp("regular file"))
		})
	})

	Context("when configured with a blobstore", func() {
		JustBeforeEach(func() {
			err := testEnvironment.CreateBlobFromAsset("dummy_package.tgz", "123")
			Expect(err).NotTo(HaveOccurred())
		})

		It("compiles and stores it to the blobstore", func() {
			result, err := agentClient.CompilePackage(agentclient.BlobRef{
				Name:        "fake",
				Version:     "1",
				BlobstoreID: "123",
				SHA1:        "236cbd31a483c3594061b00a84a80c1c182b3b20",
			}, []agentclient.BlobRef{})

			Expect(err).NotTo(HaveOccurred())

			output, err := testEnvironment.RunCommand(fmt.Sprintf("sudo stat /var/vcap/data/blobs/%s", result.BlobstoreID))
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(MatchRegexp("regular file"))
		})

		It("allows passing bare sha1 for legacy support", func() {
			_, err := agentClient.CompilePackage(agentclient.BlobRef{
				Name:        "fake",
				Version:     "1",
				BlobstoreID: "123",
				SHA1:        "236cbd31a483c3594061b00a84a80c1c182b3b20",
			}, []agentclient.BlobRef{})

			Expect(err).NotTo(HaveOccurred())

			out, err := testEnvironment.RunCommand(`sudo /bin/bash -c "zgrep 'dummy contents of dummy package file' /var/vcap/data/blobs/* | wc -l"`)
			Expect(err).NotTo(HaveOccurred(), out)
			// we expect both the original, uncompiled copy and the compiled copy of the package to exist
			Expect(strings.Trim(out, "\n")).To(Equal("2"))
		})

		It("does not skip verification when digest argument is missing", func() {
			_, err := agentClient.CompilePackage(agentclient.BlobRef{
				Name:        "fake",
				Version:     "1",
				BlobstoreID: "123",
				SHA1:        "",
			}, []agentclient.BlobRef{})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("No digest algorithm found. Supported algorithms: sha1, sha256, sha512"))
		})
	})
})

func createSHA1MultiDigest(digest string) boshcrypto.MultipleDigest {
	return boshcrypto.MustNewMultipleDigest(
		boshcrypto.NewDigest(boshcrypto.DigestAlgorithmSHA1, digest))
}
