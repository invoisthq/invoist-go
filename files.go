package invoist

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"os"
	"path/filepath"
)

// File is a document to upload: its bytes, the name it is stored under,
// and optionally its content type. Build one with a struct literal or
// with FileFromBytes, FileFromPath or FileFromReader.
type File struct {
	// Filename is the name the document is stored under, such as
	// "invoice.pdf". Empty becomes "upload".
	Filename string
	// ContentType is the file's media type, such as "application/pdf".
	// Empty means guess it from the filename's extension. The API checks
	// the bytes themselves either way and accepts PDF, PNG and JPEG.
	ContentType string
	// Data is the file's contents.
	Data []byte
}

// FileFromBytes returns a File for data, named filename.
func FileFromBytes(filename string, data []byte) File {
	return File{Filename: filename, Data: data}
}

// FileFromPath reads the file at path, named after its last element.
func FileFromPath(path string) (File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("invoist: reading %s: %w", path, err)
	}
	return File{Filename: filepath.Base(path), Data: data}, nil
}

// FileFromReader reads r to the end and returns it as a File named
// filename. The bytes are held in memory so a retry can send them again.
func FileFromReader(filename string, r io.Reader) (File, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return File{}, fmt.Errorf("invoist: reading %s: %w", filename, err)
	}
	return File{Filename: filename, Data: data}, nil
}

func (f File) contentType() string {
	if f.ContentType != "" {
		return f.ContentType
	}
	if t := mime.TypeByExtension(filepath.Ext(f.Filename)); t != "" {
		return t
	}
	return "application/octet-stream"
}

// multipartBody encodes files as multipart/form-data, each under field,
// and returns the body with its Content-Type (which carries the boundary).
func multipartBody(field string, files []File, defaultName func(i int) string) ([]byte, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for i, f := range files {
		name := f.Filename
		if name == "" {
			name = defaultName(i)
		}
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", multipart.FileContentDisposition(field, name))
		h.Set("Content-Type", f.contentType())
		part, err := w.CreatePart(h)
		if err != nil {
			return nil, "", fmt.Errorf("invoist: encoding upload: %w", err)
		}
		if _, err := part.Write(f.Data); err != nil {
			return nil, "", fmt.Errorf("invoist: encoding upload: %w", err)
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", fmt.Errorf("invoist: encoding upload: %w", err)
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

// Download is a file as the API stored it.
type Download struct {
	// Data is the file's bytes, exactly as uploaded.
	Data []byte
	// ContentType is the stored media type, such as "application/pdf".
	ContentType string
	// Filename is the name from the Content-Disposition header, when the
	// API sent one.
	Filename string
}

func downloadFrom(resp *response) *Download {
	d := &Download{Data: resp.body, ContentType: resp.header.Get("Content-Type")}
	if _, params, err := mime.ParseMediaType(resp.header.Get("Content-Disposition")); err == nil {
		d.Filename = params["filename"]
	}
	return d
}
