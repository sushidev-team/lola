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

func (s *DaemonService) ReviewPackageMark(session, packageID, previous string) (protocol.ReviewRecord, error) {
	args, _ := json.Marshal(protocol.ReviewMarkArgs{Package: packageID, Previous: previous})
	var data protocol.ReviewRecord
	err := call(protocol.Request{Cmd: protocol.CmdReviewMark, Session: session, Args: args}, shortTimeout, &data)
	return data, err
}

func (s *DaemonService) ReviewPackageDiff(session, from, to string) (protocol.DiffData, error) {
	args, _ := json.Marshal(protocol.ReviewDiffArgs{From: from, To: to})
	var data protocol.DiffData
	err := call(protocol.Request{Cmd: protocol.CmdReviewDiff, Session: session, Args: args}, longTimeout, &data)
	return data, err
}

func (s *DaemonService) ReviewPackageChecks(session, packageID string) (protocol.ReviewChecksData, error) {
	args, _ := json.Marshal(protocol.ReviewVerifyArgs{Package: packageID})
	var data protocol.ReviewChecksData
	err := call(protocol.Request{Cmd: protocol.CmdReviewChecks, Session: session, Args: args}, shortTimeout, &data)
	return data, err
}

func (s *DaemonService) ReviewPackageVerify(session string, request protocol.ReviewVerifyArgs) (protocol.ReviewCheck, error) {
	args, _ := json.Marshal(request)
	var data protocol.ReviewCheck
	err := call(protocol.Request{Cmd: protocol.CmdReviewVerify, Session: session, Args: args}, shortTimeout, &data)
	return data, err
}

func (s *DaemonService) ReviewPackageRecover(session, packageID, id string) (protocol.ReviewCheck, error) {
	args, _ := json.Marshal(protocol.ReviewVerifyArgs{Package: packageID, ID: id})
	var data protocol.ReviewCheck
	err := call(protocol.Request{Cmd: protocol.CmdReviewRecover, Session: session, Args: args}, longTimeout, &data)
	return data, err
}

func (s *DaemonService) ReviewPackageCapture(session string, request protocol.ReviewCaptureArgs) (protocol.ReviewPackage, error) {
	args, _ := json.Marshal(request)
	var data protocol.ReviewPackage
	err := call(protocol.Request{Cmd: protocol.CmdReviewCapture, Session: session, Args: args}, longTimeout, &data)
	return data, err
}
