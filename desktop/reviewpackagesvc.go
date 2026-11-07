package main

import (
	"encoding/json"
	"github.com/sushidev-team/lola/internal/protocol"
)

func (s *DaemonService) ReviewPackages(session string) (protocol.ReviewPackagesData, error) {
	var data protocol.ReviewPackagesData
	err := call(protocol.Request{Cmd: protocol.CmdReviewPackages, Session: session}, shortTimeout, &data)
	return data, err
}

func (s *DaemonService) ReviewPackageImage(session, packageID, artifact string) (protocol.ReviewImageData, error) {
	args, _ := json.Marshal(protocol.ReviewImageArgs{Package: packageID, Artifact: artifact})
	var data protocol.ReviewImageData
	err := call(protocol.Request{Cmd: protocol.CmdReviewImage, Session: session, Args: args}, shortTimeout, &data)
	return data, err
}
