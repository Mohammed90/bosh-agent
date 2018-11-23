package action_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"fmt"
	. "github.com/cloudfoundry/bosh-agent/agent/action"
	boshas "github.com/cloudfoundry/bosh-agent/agent/applier/applyspec"
	fakeas "github.com/cloudfoundry/bosh-agent/agent/applier/applyspec/fakes"
	"github.com/cloudfoundry/bosh-agent/platform/platformfakes"
	boshassert "github.com/cloudfoundry/bosh-utils/assert"
	fakesys "github.com/cloudfoundry/bosh-utils/system/fakes"
)

var (
	firstPropName  = "nats.tls.client_ca.certificate"
	secondPropName = "other.tls.client.ca.certificate"
	jobNames       = []string{
		"fake-job",
		"another-fake-job",
	}
)

var _ = FDescribe("GetCertInfo", func() {
	var (
		action           GetCertInfoAction
		fileSystem       *fakesys.FakeFileSystem
		platform         *platformfakes.FakePlatform
		specService      *fakeas.FakeV1Service
		certsFileContent = ""
	)

	BeforeEach(func() {
		platform = &platformfakes.FakePlatform{}
		fileSystem = fakesys.NewFakeFileSystem()
		platform.GetFsReturns(fileSystem)
		specService = fakeas.NewFakeV1Service()
		action = NewGetCertInfoTask(specService, fileSystem)

		certsFileContent = ""
		for _, job := range jobNames {
			err := fileSystem.MkdirAll(fmt.Sprintf("/var/vcap/jobs/%s", job), 0700)
			Expect(err).NotTo(HaveOccurred())
			err = fileSystem.WriteFileString(certFilePath(job), certsFileContent)
			Expect(err).NotTo(HaveOccurred())
		}
	})

	AssertActionIsNotAsynchronous(action)
	AssertActionIsNotPersistent(action)
	AssertActionIsLoggable(action)

	AssertActionIsNotResumable(action)
	AssertActionIsNotCancelable(action)

	Context("when certificate file for validation exist in job", func() {

		BeforeEach(func() {
			specService.Spec = boshas.V1ApplySpec{
				RenderedTemplatesArchiveSpec: &boshas.RenderedTemplatesArchiveSpec{},
				JobSpec: boshas.JobSpec{
					JobTemplateSpecs: []boshas.JobTemplateSpec{
						{Name: "fake-job"},
						{Name: "another-fake-job"},
					},
				},
			}
		})

		Context("when certs are valid", func() {
			BeforeEach(func() {
				for _, job := range jobNames {
					certsFileContent = fakeCert(firstPropName, true)
					err := fileSystem.WriteFileString(certFilePath(job), certsFileContent)
					Expect(err).NotTo(HaveOccurred())
				}
			})

			It("returns certificate details per job", func() {

				taskValue, err := action.Run()
				Expect(err).ToNot(HaveOccurred())

				// Check JSON key casing
				boshassert.MatchesJSONString(GinkgoT(), taskValue,
					`{"another-fake-job":[{"property":"nats.tls.client_ca.certificate","expires":1574372638,"error_string":""}],"fake-job":[{"property":"nats.tls.client_ca.certificate","expires":1574372638,"error_string":""}]}`)
			})
		})

		Context("when certs are not parseable", func() {
			BeforeEach(func() {
				for _, job := range jobNames {
					certsFileContent = fakeCert("nats.tls.client_ca.certificate", false)
					err := fileSystem.WriteFileString(certFilePath(job), certsFileContent)
					Expect(err).NotTo(HaveOccurred())
				}
			})

			It("should return error details to the value", func() {
				taskValue, err := action.Run()
				Expect(err).ToNot(HaveOccurred())

				boshassert.MatchesJSONString(GinkgoT(), taskValue,
					`{"another-fake-job":[{"property":"nats.tls.client_ca.certificate","expires":0,"error_string":"failed to decode certificate"}],"fake-job":[{"property":"nats.tls.client_ca.certificate","expires":0,"error_string":"failed to decode certificate"}]}`)
			})
		})

		Context("When there are no certificates on job", func() {
			It("should return the job name, with an empty array", func() {
				taskValue, err := action.Run()
				Expect(err).ToNot(HaveOccurred())

				boshassert.MatchesJSONString(GinkgoT(), taskValue, `{"another-fake-job":[],"fake-job":[]}`)
			})
		})

		Context("when there are both parseable and unparseable certs present", func() {
			BeforeEach(func() {
				for _, job := range jobNames {
					certsFileContent = fakeCert("this.is.bad", false) + "\n" +
						fakeCert(firstPropName, true) + "\n" +
						fakeCert(secondPropName, true)
					err := fileSystem.WriteFileString(certFilePath(job), certsFileContent)
					Expect(err).NotTo(HaveOccurred())
				}
			})

			//TODO: the JSON returns with elements in different order
			XIt("should return the expiry date for the valid certs and errors for the invalid certs", func() {
				taskValue, err := action.Run()
				Expect(err).ToNot(HaveOccurred())

				boshassert.MatchesJSONString(GinkgoT(), taskValue,
					`{"another-fake-job":[{"property":"other.tls.client.ca.certificate","expires":1574372638,"error_string":""},{"property":"this.is.bad","expires":0,"error_string":"failed to decode certificate"},{"property":"nats.tls.client_ca.certificate","expires":1574372638,"error_string":""}],"fake-job":[{"property":"this.is.bad","expires":0,"error_string":"failed to decode certificate"},{"property":"nats.tls.client_ca.certificate","expires":1574372638,"error_string":""},{"property":"other.tls.client.ca.certificate","expires":1574372638,"error_string":""}]}`)
			})
		})

		XContext("When file cannot be read", func() {
			BeforeEach(func() {
				for _, job := range jobNames {
					certsFileContent = fakeCert(firstPropName, true)
					err := fileSystem.WriteFileString(certFilePath(job), certsFileContent)
					Expect(err).NotTo(HaveOccurred())
				}
			})

			It("should return an error stating that file could not be read", func() {
				for _, job := range jobNames {
					err := fileSystem.Chmod(certFilePath(job), 0055)
					Expect(err).NotTo(HaveOccurred())
				}
				taskValue, err := action.Run()
				Expect(err).ToNot(HaveOccurred())

				boshassert.MatchesJSONString(GinkgoT(), taskValue,
					`{"fake-job":[]}`)
			})
		})

		Context("When yaml file content cannot be unmarshaled", func() {
			BeforeEach(func() {
				for _, job := range jobNames {
					certsFileContent = firstPropName + ": |\nTHIS NO WORK"
					err := fileSystem.WriteFileString(certFilePath(job), certsFileContent)
					Expect(err).NotTo(HaveOccurred())
				}
			})

			It("should return an error indicating the unmarsheable yaml", func() {
				_, err := action.Run()
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Unmarshaling YAML for"))
			})
		})
	})

	Context("when certificate file for validation does not exist in job", func() {

		BeforeEach(func() {
			specService.Spec = boshas.V1ApplySpec{
				RenderedTemplatesArchiveSpec: &boshas.RenderedTemplatesArchiveSpec{},
				JobSpec: boshas.JobSpec{
					JobTemplateSpecs: []boshas.JobTemplateSpec{
						{Name: "fake-job"},
					},
				},
			}
		})

		It("should return an error", func() {
			for _, job := range jobNames {
				fileSystem.RemoveAll(certFilePath(job))
			}
			_, err := action.Run()
			Expect(err).To(HaveOccurred())

			Expect(err.Error()).To(ContainSubstring("not found"))
		})
	})
})

func fakeCert(propName string, valid bool) string {
	goodCert := `
  -----BEGIN CERTIFICATE-----
  MIIEijCCAvKgAwIBAgIRAKlv5BEguA9GrlrfUVeWwAcwDQYJKoZIhvcNAQELBQAw
  TjEMMAoGA1UEBhMDVVNBMRYwFAYDVQQKEw1DbG91ZCBGb3VuZHJ5MSYwJAYDVQQD
  Ex1kZWZhdWx0Lm5hdHMtY2EuYm9zaC1pbnRlcm5hbDAeFw0xODExMjEyMTQzNTha
  Fw0xOTExMjEyMTQzNThaME4xDDAKBgNVBAYTA1VTQTEWMBQGA1UEChMNQ2xvdWQg
  Rm91bmRyeTEmMCQGA1UEAxMdZGVmYXVsdC5uYXRzLWNhLmJvc2gtaW50ZXJuYWww
  ggGiMA0GCSqGSIb3DQEBAQUAA4IBjwAwggGKAoIBgQDTM7eDeiesG1zZKGHWZdSd
  ZQMun/LmVwRCVlLFoutJj+78xoujrh0hMzQ1nHXsvI7kEmlvQfo1KmYTmWpiIgG9
  pVXHcsZgwDU+9ZCf4zrl0bTVHLLpkUX1c7FW2ptu1CxLdS8tp9Shk1OMqKL1oYcz
  63rVww1nso5nHZDt0Ew81fBdWLk34GPST9RlEUXh7r7IetInA9V1p/65hljj1gsG
  wIoqOdpdw3xj9BFt3TxUGtYdeC4PfVyxBl2I7w4w9PDTY84LSnGo6HDSBW43iU4k
  x1Cu922G265IMf4w2be51ZyoCkZnHOjb+Wo66ePfJ0Qg7bPHhZuNoqY4df6HAGyn
  MPQWJPORT3+/Ri6LLOTF1tghLGjBzWNaAkzfmAPHcCWgWc5WHwlTxmBPYtrts1Vg
  9ibOAdcaWz7S4n7FVk7Dh8Npi7RF0Ho8o6MDbcSDDowqlLqXYmieqzAjfCPKNtvk
  M5cJ4RCAtG5Po15JOE4HshwfE6gbc5yyLi8RcuWXacUCAwEAAaNjMGEwDgYDVR0P
  AQH/BAQDAgEGMA8GA1UdEwEB/wQFMAMBAf8wHQYDVR0OBBYEFAgZx38UBXPQmtHU
  622eUCkz/97AMB8GA1UdIwQYMBaAFAgZx38UBXPQmtHU622eUCkz/97AMA0GCSqG
  SIb3DQEBCwUAA4IBgQDK6RJOG5AyaAi0VfPJiS1wX3J50mk6ui9krPUTrsE1pmSe
  jkluGVPtN66RWXggRjIvnV6C8ICKEOpkwvm2AHkWIxwjM9v76cWCoJs9iYX+BVr8
  IVOlkG/UY0rh6KIOEvS6dKgZbqSTtd1GB6iwini/BUSyIFQmYaDVrzjO/I6RAEnB
  HVWWM+yJ7uekKf55krQ85LuXIJYg/KugGyM3rnmiDu8unemSeUYDllJaPimxAsTO
  rZFz7paCLh5SF4ntNBsymO55vL2NTRE/D7PtUd41yQjGUlJmxzvEFdRUPo/1fcS4
  VluN6ZrYe5iS39c3o72T+dgLxWBo4XL8Ynfet6CD+BkZKTO8H0v2zKDhnq6tlvMu
  QqoEHFQ6x7sEn+SAACpV4Z+MMaWtrnzfG96DyyTtk1M1MLQowTjown4orABSuNn9
  5ka/AP3rwlh66oK1ktwmClpnNPkUumj9wPtyPS/AH04IjeIKfqO9JTPKg0VdEfOT
  LYlKT1StItAfXfZyfZs=
  -----END CERTIFICATE-----
`
	if valid {
		return propName + ": |\n  " + goodCert
	} else {
		return propName + ": |\n  UNPARSEABLE CERT"
	}
}

func certFilePath(jobName string) string {
	return fmt.Sprintf("/var/vcap/jobs/%s/config/validate_certificate.yml", jobName)
}
