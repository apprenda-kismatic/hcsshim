package hcsshim

import (
	"io"
	"runtime"
	"syscall"

	"github.com/Microsoft/go-winio"
	"github.com/Sirupsen/logrus"
)

// ExportLayer will create a folder at exportFolderPath and fill that folder with
// the transport format version of the layer identified by layerId. This transport
// format includes any metadata required for later importing the layer (using
// ImportLayer), and requires the full list of parent layer paths in order to
// perform the export.
func ExportLayer(info DriverInfo, layerId string, exportFolderPath string, parentLayerPaths []string) error {
	title := "hcsshim::ExportLayer "
	logrus.Debugf(title+"flavour %d layerId %s folder %s", info.Flavour, layerId, exportFolderPath)

	// Generate layer descriptors
	layers, err := layerPathsToDescriptors(parentLayerPaths)
	if err != nil {
		return err
	}

	// Convert info to API calling convention
	infop, err := convertDriverInfo(info)
	if err != nil {
		logrus.Error(err)
		return err
	}

	err = exportLayer(&infop, layerId, exportFolderPath, layers)
	if err != nil {
		err = makeErrorf(err, title, "layerId=%s flavour=%d folder=%s", layerId, info.Flavour, exportFolderPath)
		logrus.Error(err)
		return err
	}

	logrus.Debugf(title+"succeeded flavour=%d layerId=%s folder=%s", info.Flavour, layerId, exportFolderPath)
	return nil
}

// LayerReader provides an interface for extracting the contents of an on-disk layer.
type LayerReader struct {
	context uintptr
}

// Next reads the next available file from a layer, ensuring that parent directories are always read
// before child files and directories.
//
// Next returns the file's relative path, size, and basic file metadata. Read() should be used to
// extract a Win32 backup stream with the remainder of the metadata and the data.
func (r *LayerReader) Next() (string, int64, *winio.FileBasicInfo, error) {
	var fileNamep *uint16
	fileInfo := &winio.FileBasicInfo{}
	var deleted uint32
	var fileSize int64
	err := exportLayerNext(r.context, &fileNamep, fileInfo, &fileSize, &deleted)
	if err != nil {
		if err == syscall.ERROR_NO_MORE_FILES {
			err = io.EOF
		} else {
			err = makeError(err, "ExportLayerNext", "")
		}
		return "", 0, nil, err
	}
	fileName := convertAndFreeCoTaskMemString(fileNamep)
	if deleted != 0 {
		fileInfo = nil
	}
	if fileName[0] == '\\' {
		fileName = fileName[1:]
	}
	return fileName, fileSize, fileInfo, nil
}

// Read reads from the current file's Win32 backup stream.
func (r *LayerReader) Read(b []byte) (int, error) {
	var bytesRead uint32
	err := exportLayerRead(r.context, b, &bytesRead)
	if err != nil {
		return 0, makeError(err, "ExportLayerRead", "")
	}
	if bytesRead == 0 {
		return 0, io.EOF
	}
	return int(bytesRead), nil
}

// Close frees resources associated with the layer reader. It will return an
// error if there was an error while reading the layer or of the layer was not
// completely read.
func (r *LayerReader) Close() (err error) {
	if r.context != 0 {
		err = exportLayerEnd(r.context)
		if err != nil {
			err = makeError(err, "ExportLayerEnd", "")
		}
		r.context = 0
	}
	return
}

// NewLayerReader returns a new layer reader for reading the contents of an on-disk layer.
func NewLayerReader(info DriverInfo, layerId string, parentLayerPaths []string) (*LayerReader, error) {
	layers, err := layerPathsToDescriptors(parentLayerPaths)
	if err != nil {
		return nil, err
	}
	infop, err := convertDriverInfo(info)
	if err != nil {
		return nil, err
	}
	r := &LayerReader{}
	err = exportLayerBegin(&infop, layerId, layers, &r.context)
	if err != nil {
		return nil, makeError(err, "ExportLayerBegin", "")
	}
	runtime.SetFinalizer(r, func(r *LayerReader) { r.Close() })
	return r, err
}
