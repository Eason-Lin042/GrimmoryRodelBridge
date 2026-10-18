package epub

import "os"

func createPrivate(filename string) (*os.File, error) {
	return os.OpenFile(filename, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
}
