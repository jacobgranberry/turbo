// Package graph contains the CompleteGraph struct and some methods around it
package graph

import (
	gocontext "context"
	"fmt"

	"github.com/pyr-sh/dag"
	"github.com/vercel/turbo/cli/internal/fs"
	"github.com/vercel/turbo/cli/internal/nodes"
	"github.com/vercel/turbo/cli/internal/turbopath"
	"github.com/vercel/turbo/cli/internal/util"
)

// WorkspaceInfos holds information about each workspace in the monorepo.
type WorkspaceInfos map[string]*fs.PackageJSON

// CompleteGraph represents the common state inferred from the filesystem and pipeline.
// It is not intended to include information specific to a particular run.
type CompleteGraph struct {
	// WorkspaceGraph expresses the dependencies between packages
	WorkspaceGraph dag.AcyclicGraph

	// Pipeline is config from turbo.json
	Pipeline fs.Pipeline

	// WorkspaceInfos stores the package.json contents by package name
	WorkspaceInfos WorkspaceInfos

	// GlobalHash is the hash of all global dependencies
	GlobalHash string

	RootNode string
}

// GetPackageTaskVisitor wraps a `visitor` function that is used for walking the TaskGraph
// during execution (or dry-runs). The function returned here does not execute any tasks itself,
// but it helps curry some data from the Complete Graph and pass it into the visitor function.
func (g *CompleteGraph) GetPackageTaskVisitor(ctx gocontext.Context, visitor func(ctx gocontext.Context, packageTask *nodes.PackageTask) error) func(taskID string) error {
	return func(taskID string) error {
		packageName, taskName := util.GetPackageTaskFromId(taskID)

		pkg, ok := g.WorkspaceInfos[packageName]
		if !ok {
			return fmt.Errorf("cannot find package %v for task %v", packageName, taskID)
		}

		// Get the TaskDefinition from the Root turbo.json
		// We'll store this separately for now, because we'll get the rest of our task
		// definitions in reverse order, and we want to make sure to overwrite in the right order.
		rootTaskDefinition, err := getTaskFromPipeline(g.Pipeline, taskID, taskName)
		// If we don't find a taskDefinition from this taskID and name
		// in the root pipeline we're in trouble.
		if err != nil {
			return err
		}

		// Start a list of TaskDefinitions we've found for this TaskID
		taskDefinitions := []fs.TaskDefinition{}

		// Start in the workspace directory
		// Find the closest turbo.json as we iterate up
		// TODO: where does this Findup end?
		directory := turbopath.AbsoluteSystemPath(pkg.Dir)
		turboJSONPath, findTurboJSONErr := directory.Findup("turbo.json")
		if findTurboJSONErr != nil {
			return findTurboJSONErr
		}

		// For loop until we break manually.
		// We will reassign `turboJSONPath` inside this loop, so that
		// every time we iterate, we're starting from a new one.
		for {
			turboJSON, err := fs.ReadTurboConfiFromPath(turboJSONPath)
			if err != nil {
				return err
			}

			pipeline := turboJSON.Pipeline
			taskDefinition, err := getTaskFromPipeline(pipeline, taskID, taskName)
			if err != nil {
				// we don't need to do anything if no taskDefinition was found in this pipeline
			} else {
				// Add it into the taskDefinitions
				taskDefinitions = append(taskDefinitions, taskDefinition)

				// If this turboJSON doesn't have an extends property, we can stop our for loop here.
				if turboJSON.Extends == "" {
					break
				}

				// If there's an extends property, walk up to the next one
				// Find the workspace it refers to, and and assign `directory` to it for the
				// next iteration in this for loop.
				workspace, ok := g.WorkspaceInfos[turboJSON.Extends]
				if !ok {
					// TODO: Should this be a hard error?
					// A workspace was referenced that doesn't exist or we know nothing about
					break
				}

				directory = turbopath.AbsoluteSystemPath(workspace.Dir)

				// Reassign this. The loop will run again with this new turbo.json now.
				turboJSONPath = directory.UntypedJoin("turbo.json")
			}
		}

		// Create this final taskDefinition
		// For now copy over the rootTaskDefinition
		// TODO: how to do this without knowing about every field in the struct??
		mergedTaskDefinition := &fs.TaskDefinition{
			Outputs:                 rootTaskDefinition.Outputs,
			ShouldCache:             rootTaskDefinition.ShouldCache,
			EnvVarDependencies:      rootTaskDefinition.EnvVarDependencies,
			TopologicalDependencies: rootTaskDefinition.TopologicalDependencies,
			TaskDependencies:        rootTaskDefinition.TaskDependencies,
			Inputs:                  rootTaskDefinition.Inputs,
			OutputMode:              rootTaskDefinition.OutputMode,
			Persistent:              rootTaskDefinition.Persistent,
		}

		// TODO: Iterate through all the taskDefinitions in reverse order!
		// We need to reverse them because we started from the package's workspace
		// but we'll start overwriting the rootTurboJSON as we follow the extends chain
		// in from the outside. That way the innermost (the turbo.json in the workspace)
		// will overwrite the taskDefinition last, and we'll have the right hierarchy
		// sort.Reverse(taskDefinitions)
		for _, taskDef := range taskDefinitions {
			// merge the thing
			// Need to iterate through all the NON_EMPTY fields in the struct
			// and assign them to the mergedTaskDefinition
			// If it's an array or object, we need to _append_.
		}

		packageTask := &nodes.PackageTask{
			TaskID:         taskID,
			Task:           taskName,
			PackageName:    packageName,
			Pkg:            pkg,
			TaskDefinition: mergedTaskDefinition,
		}

		return visitor(ctx, packageTask)
	}
}

func getTaskFromPipeline(pipeline fs.Pipeline, taskID string, taskName string) (fs.TaskDefinition, error) {
	// first check for package-tasks
	taskDefinition, ok := pipeline[taskID]
	if !ok {
		// then check for regular tasks
		fallbackTaskDefinition, notcool := pipeline[taskName]
		// if neither, then bail
		if !notcool {
			// TODO: the compiler doesn't like this nil return here, can't
			// remember how to do an empty return for a struct...
			return nil, fmt.Errorf("No task defined in pipeline")
		}

		// override if we need to...
		taskDefinition = fallbackTaskDefinition
	}

	return taskDefinition, nil
}
