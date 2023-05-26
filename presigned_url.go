package courier

import (
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

func PresignedURL(link string, accessKey string, secretKey string, region string) (string, error) {

	splitURL := strings.Split(link, ".")
	bucketName := strings.TrimPrefix(splitURL[0], "https://")

	splitURL = strings.Split(link, "attachments")
	objectKey := "/attachments" + splitURL[0]

	sess, err := session.NewSession(&aws.Config{
		Credentials: credentials.NewStaticCredentials(accessKey, secretKey, ""),
		Region:      aws.String(region),
	})
	if err != nil {
		return "", err
	}

	svc := s3.New(sess)

	req, _ := svc.GetObjectRequest(&s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectKey),
	})
	urlStr, err := req.Presign((24 * time.Hour) * 7)

	if err != nil {
		return "", err
	}

	return urlStr, nil

}
