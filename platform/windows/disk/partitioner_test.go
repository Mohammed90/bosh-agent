package disk_test

import (
	"errors"
	"fmt"

	"strings"

	"github.com/cloudfoundry/bosh-agent/platform/windows/disk"
	"github.com/cloudfoundry/bosh-utils/system/fakes"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Partitioner", func() {
	const cmdStandardError = `Get-Disk : No MSFT_Disk objects found with property 'Number' equal to '0'.
Verify the value of the property and retry.
At line:1 char:1
+ Get-Disk 0
+ ~~~~~~~~~~
    + CategoryInfo          : ObjectNotFound: (0:UInt32) [Get-Disk], CimJobExc
   eption
    + FullyQualifiedErrorId : CmdletizationQuery_NotFound_Number,Get-Disk
`

	var (
		cmdRunner   *fakes.FakeCmdRunner
		partitioner *disk.Partitioner
		diskNumber  string
	)

	BeforeEach(func() {
		cmdRunner = fakes.NewFakeCmdRunner()
		partitioner = &disk.Partitioner{
			Runner: cmdRunner,
		}
		diskNumber = "1"
	})

	Describe("GetFreeSpaceOnDisk", func() {
		It("returns the free space on disk", func() {
			expectedFreeSpace := 5 * 1024 * 1024 * 1024

			cmdRunner.AddCmdResult(
				partitionFreeSpaceCommand(diskNumber),
				fakes.FakeCmdResult{
					Stdout: fmt.Sprintf(`%d
`, expectedFreeSpace),
				},
			)

			freeSpace, err := partitioner.GetFreeSpaceOnDisk(diskNumber)
			Expect(err).NotTo(HaveOccurred())
			Expect(freeSpace).To(Equal(expectedFreeSpace))

		})

		It("when the command fails returns a wrapped error", func() {
			cmdRunnerError := errors.New("It went wrong")
			cmdRunner.AddCmdResult(
				partitionFreeSpaceCommand(diskNumber),
				fakes.FakeCmdResult{ExitStatus: -1, Error: cmdRunnerError},
			)

			_, err := partitioner.GetFreeSpaceOnDisk(diskNumber)
			Expect(err).To(MatchError(fmt.Sprintf(
				"failed to find free space on disk %s: %s",
				diskNumber,
				cmdRunnerError.Error(),
			)))
		})

		It("when response of command is not a number, returns an informative error", func() {
			freeSpaceCommand := partitionFreeSpaceCommand(diskNumber)
			expectedStdout := `Not a number
`

			cmdRunner.AddCmdResult(
				freeSpaceCommand,
				fakes.FakeCmdResult{
					Stdout: expectedStdout,
				},
			)

			_, err := partitioner.GetFreeSpaceOnDisk(diskNumber)
			Expect(err).To(MatchError(fmt.Sprintf(
				"Failed to convert output of \"%s\" command in to number. Output was: \"%s\"",
				freeSpaceCommand,
				strings.TrimSpace(expectedStdout),
			)))
		})
	})

	Describe("GetCountOnDisk", func() {
		It("returns number of partitions found on disk", func() {
			expectedPartitionCount := "2"

			cmdRunner.AddCmdResult(
				partitionCountCommand(diskNumber),
				fakes.FakeCmdResult{
					Stdout: fmt.Sprintf(`%s
`, expectedPartitionCount),
				},
			)

			partitionCount, err := partitioner.GetCountOnDisk(diskNumber)
			Expect(err).NotTo(HaveOccurred())
			Expect(partitionCount).To(Equal(expectedPartitionCount))
		})

		It("when the command fails returns a wrapped error", func() {
			cmdRunnerError := errors.New("It went wrong")
			cmdRunner.AddCmdResult(
				partitionCountCommand(diskNumber),
				fakes.FakeCmdResult{ExitStatus: -1, Error: cmdRunnerError},
			)

			_, err := partitioner.GetCountOnDisk(diskNumber)
			Expect(err).To(MatchError(fmt.Sprintf(
				"failed to get existing partition count for disk %s: %s",
				diskNumber,
				cmdRunnerError.Error(),
			)))
		})
	})

	Describe("InitializeDisk", func() {
		It("makes the request to initialize the given disk", func() {
			expectedCommand := initializeDiskCommand(diskNumber)

			cmdRunner.AddCmdResult(expectedCommand, fakes.FakeCmdResult{})

			err := partitioner.InitializeDisk(diskNumber)
			Expect(err).NotTo(HaveOccurred())
			Expect(cmdRunner.RunCommands).To(Equal([][]string{strings.Split(expectedCommand, " ")}))
		})

		It("when the command fails returns a wrapped error", func() {
			cmdRunnerError := errors.New("It went wrong")
			cmdRunner.AddCmdResult(
				initializeDiskCommand(diskNumber),
				fakes.FakeCmdResult{ExitStatus: -1, Error: cmdRunnerError},
			)

			err := partitioner.InitializeDisk(diskNumber)
			Expect(err).To(MatchError(fmt.Sprintf("failed to initialize disk %s: %s", diskNumber, cmdRunnerError)))
		})
	})
})

func partitionCountCommand(diskNumber string) string {
	return fmt.Sprintf("Get-Disk -Number %s | Select -ExpandProperty NumberOfPartitions", diskNumber)
}

func partitionFreeSpaceCommand(diskNumber string) string {
	return fmt.Sprintf("Get-Disk %s | Select -ExpandProperty LargestFreeExtent", diskNumber)
}

func initializeDiskCommand(diskNumber string) string {
	return fmt.Sprintf("Initialize-Disk -Number %s -PartitionStyle GPT", diskNumber)
}