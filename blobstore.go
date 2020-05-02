package main

import (
	"cloud.google.com/go/iam/credentials/apiv1"
	"cloud.google.com/go/storage"
	"context"
	"errors"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/iterator"
	credentials2 "google.golang.org/genproto/googleapis/iam/credentials/v1"
	"log"
	"time"
)

var (
	ErrFileNotFound = errors.New("specified file could not be found")
)

type File struct {
	Name        string
	IsDirectory bool
	Path        string
}

type BlobStore struct {
	context       context.Context
	storageClient storage.Client
	bucketName    string
}

func (service BlobStore) Files(name string) ([]File, error) {
	bucket := service.storageClient.Bucket(service.bucketName)
	query := &storage.Query{
		Delimiter: "/",
		Prefix:    name,
	}

	var files []File
	log.Printf("Requesting objects matching %s\n", name)
	it := bucket.Objects(service.context, query)
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		if attrs.Prefix != "" {
			files = append(files, File{
				Name:        attrs.Prefix,
				IsDirectory: true,
				Path:        "/" + attrs.Prefix,
			})
		} else if attrs.Name != name {
			files = append(files, File{
				Name: attrs.Name,
				Path: "/" + attrs.Name,
			})
		}
	}
	return files, nil
}

func (service BlobStore) File(name string) (File, error) {
	bucket := service.storageClient.Bucket(service.bucketName)
	object := bucket.Object(name)

	log.Printf("Retrieve attributes for object %s\n", name)
	attrs, err := object.Attrs(service.context)
	if err != nil {
		switch err {
		default:
			return File{}, err
		case storage.ErrObjectNotExist:
			return File{}, ErrFileNotFound
		}
	}

	oneDay, err := time.ParseDuration("24h")
	if err != nil {
		return File{}, err
	}

	log.Println("Retrieving service account credentials.")
	creds, err := google.FindDefaultCredentials(service.context, storage.ScopeFullControl)
	if err != nil {
		return File{}, err
	}

	token, _ := creds.TokenSource.Token()
	log.Println("Token info: ", token)
	log.Println("Decoding JSON")
	conf, err := google.JWTConfigFromJSON(creds.JSON)
	if err != nil {
		return File{}, err
	}

	opts := storage.SignedURLOptions{
		Expires:        time.Now().Add(oneDay),
		GoogleAccessID: conf.Email,
		Method:         "GET",
		Scheme:         storage.SigningSchemeV4,
	}

	if conf.PrivateKey == nil || len(conf.PrivateKey) == 0 {
		opts.SignBytes = signBytes(conf.Email, service.context)
	} else {
		opts.PrivateKey = conf.PrivateKey
	}

	log.Println("Requesting signed URL for object.")
	signedUrl, err := storage.SignedURL(service.bucketName, name, &opts)
	if err != nil {
		return File{}, err
	}

	return File{
		Name: attrs.Name,
		Path: signedUrl,
	}, nil
}

func signBytes(account string, context context.Context) func([]byte) ([]byte, error) {
	return func(bytes []byte) ([]byte, error) {
		client, err := credentials.NewIamCredentialsClient(context)
		if err != nil {
			return nil, err
		}
		name := "projects/-/serviceAccounts/" + account

		log.Println("Signing blob for service account.")
		resp, err := client.SignBlob(context, &credentials2.SignBlobRequest{
			Name: name,
			Delegates: []string{
				name,
			},
			Payload: bytes,
		})
		if err != nil {
			return nil, err
		}
		return resp.SignedBlob, nil
	}
}
