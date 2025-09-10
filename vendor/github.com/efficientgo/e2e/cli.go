// Copyright (c) The EfficientGo Authors.
// Licensed under the Apache License 2.0.

package e2e

import "strings"

func EmptyFlags() map[string]string {
	return map[string]string{}
}

func MergeFlags(inputs ...map[string]string) map[string]string {
	output := MergeFlagsWithoutRemovingEmpty(inputs...)

	for k, v := range output {
		if v == "" {
			delete(output, k)
		}
	}
	return output
}

func MergeFlagsWithoutRemovingEmpty(inputs ...map[string]string) map[string]string {
	output := map[string]string{}

	for _, input := range inputs {
		for name, value := range input {
			output[name] = value
		}
	}
	return output
}

func BuildArgs(flags map[string]string) []string {
	args := make([]string, 0, len(flags))

	for name, value := range flags {
		if value != "" {
			args = append(args, name+"="+value)
			continue
		}
		args = append(args, name)
	}
	return args
}

// BuildKingpinArgs is like BuildArgs but with special handling of slice args.
// NOTE(bwplotka): flags with values as comma but not indented to be slice will cause issues.
func BuildKingpinArgs(flags map[string]string) []string {
	args := make([]string, 0, len(flags))

	for name, value := range flags {
		if value != "" {
			s := strings.Split(value, ",")
			for _, ss := range s {
				args = append(args, name+"="+ss)
			}
			continue
		}
		args = append(args, name)
	}
	return args
}
