package cli

import (
	"io"
	"os"
)

// Clear only a real interactive console; logs and redirected output stay readable.
func clearPromptScreen(output io.Writer) {
	file, ok := output.(*os.File)
	if !ok || os.Getenv("TERM") == "dumb" {
		return
	}
	clearConsole(file)
}

func interactiveConsole(input io.Reader, output io.Writer) bool {
	inputFile, inputOK := input.(*os.File)
	outputFile, outputOK := output.(*os.File)
	return inputOK && outputOK && terminalInput(inputFile) && terminalInput(outputFile)
}
