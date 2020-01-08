// THIS FILE IS PART OF GQLENGINE PROJECT, COPYRIGHTS BELONGS TO 凯斐德科技（杭州）有限公司.
package gqlengine

import (
	"context"
	"fmt"
	"reflect"

	"github.com/karfield/graphql"
)

type resolverArgumentBuilder interface {
	build(params graphql.ResolveParams) (reflect.Value, error)
}

const (
	returnResult = iota + 1
	returnError
	returnContext
)

type resolver struct {
	fn             graphql.ResolveFieldWithContext
	fnPrototype    reflect.Value
	args           reflect.Type
	argsInfo       *unwrappedInfo
	argConfig      graphql.FieldConfigArgument
	argBuilders    []resolverArgumentBuilder
	source         reflect.Type
	sourceInfo     *unwrappedInfo
	out            reflect.Type
	outInfo        *unwrappedInfo
	resultBuilders []int
	isBatch        bool
}

func (r resolver) buildArgs(p graphql.ResolveParams) ([]reflect.Value, error) {
	args := make([]reflect.Value, len(r.argBuilders))
	for i, ab := range r.argBuilders {
		arg, err := ab.build(p)
		if err != nil {
			return nil, err
		}
		args[i] = arg
	}
	return args, nil
}

func (r resolver) buildResults(ctx context.Context, outs []reflect.Value) (interface{}, context.Context, error) {
	var (
		result interface{}
		err    error
	)

	for i, res := range outs {
		switch r.resultBuilders[i] {
		case returnResult:
			result = res.Interface()
		case returnContext:
			ctx = context.WithValue(ctx, res.Type(), res.Interface())
		case returnError:
			if !res.IsNil() {
				err = res.Interface().(error)
			}
		}
	}
	return result, ctx, err
}

func checkResultType(expected, actually reflect.Type) bool {
	// unwrap slice
	if expected.Kind() == reflect.Slice {
		if actually.Kind() != reflect.Slice {
			return false
		}
		expected = expected.Elem()
		actually = actually.Elem()
	} else if actually.Kind() == reflect.Slice {
		return false
	}

	if expected.Kind() == reflect.Ptr {
		expected = expected.Elem()
	}
	if actually.Kind() == reflect.Ptr {
		actually = actually.Elem()
	}

	return expected == actually
}

type (
	resolveResultChecker func(p reflect.Type) (*unwrappedInfo, error)
)

func (engine *Engine) analysisResolver(fieldName string, resolve interface{}) (*resolver, error) {
	resolveFn := reflect.ValueOf(resolve)
	resolveFnType := resolveFn.Type()
	if resolveFnType.Kind() != reflect.Func {
		panic("resolve prototype should be a function")
	}

	resolver := resolver{}

	argumentBuilders := make([]resolverArgumentBuilder, resolveFnType.NumIn())
	returnTypes := make([]int, resolveFnType.NumOut())

	for i := 0; i < resolveFnType.NumIn(); i++ {
		in := resolveFnType.In(i)
		var builder resolverArgumentBuilder
		if argsBuilder, info, err := engine.asArguments(in); err != nil || argsBuilder != nil {
			if err != nil {
				return nil, err
			}
			builder = argsBuilder
			if resolver.args != nil {
				return nil, fmt.Errorf("more than one 'arguments' parameter[%d]", i)
			}
			resolver.args = in
			resolver.argsInfo = info
		} else if ctxBuilder, err := engine.asContextArgument(in); err != nil || ctxBuilder != nil {
			if err != nil {
				return nil, err
			}
			builder = ctxBuilder
		} else if objSource, info, err := engine.asObjectSource(in); err != nil || objSource != nil {
			if err != nil {
				return nil, err
			}
			builder = objSource
			if resolver.source == nil {
				resolver.source = in
			} else {
				return nil, fmt.Errorf("more than one source argument[%d]: '%s'", i, in)
			}
			resolver.isBatch = info.array
			resolver.sourceInfo = info
		} else { // fixme: add selection set builder
			return nil, fmt.Errorf("unsupported argument type [%d]: '%s'", i, in)
		}
		argumentBuilders[i] = builder
	}

	var sourceField *reflect.StructField
	if resolver.source != nil {
		if fieldName == "" {
			return nil, fmt.Errorf("unexpect source argument '%s'", resolver.source)
		}
		srcStructType := resolver.source
		if srcStructType.Kind() == reflect.Ptr {
			srcStructType = srcStructType.Elem()
		}
		for i := 0; i < srcStructType.NumField(); i++ {
			f := srcStructType.Field(i)
			if f.Name == fieldName {
				sourceField = &f
			}
		}
		if sourceField != nil {
			if !needBeResolved(sourceField) {
				return nil, fmt.Errorf("the field need not be resolved")
			}
		}
	}

	for i := 0; i < resolveFnType.NumOut(); i++ {
		out := resolveFnType.Out(i)
		var (
			returnType int
			outInfo    *unwrappedInfo
		)
		if isContext, err := engine.asContextMerger(out); isContext {
			returnType = returnContext
		} else if err != nil {
			return nil, err
		} else if engine.asErrorResult(out) {
			returnType = returnError
		} else {
			if resolver.isBatch {
				if out.Kind() != reflect.Slice {
					return nil, fmt.Errorf("expect slice of results, but '%s' in result[%d]", out, i)
				}
				out = out.Elem() // unwrap the slice
			}
			if sourceField != nil {
				// compare out with sourceField.Type
				if !checkResultType(sourceField.Type, out) {
					return nil, fmt.Errorf("result type('%d') of resolve function is not match with field('%s') type('%s') of object",
						out, sourceField.Name, sourceField.Type)
				}
			}

			for _, check := range engine.resultCheckers {
				if info, err := check(out); err != nil {
					return nil, err
				} else if info != nil {
					outInfo = info
					break
				}
			}

			if outInfo == nil {
				return nil, fmt.Errorf("unsupported resolve result[%d] '%s'", i, out)
			}

			returnType = returnResult
		}

		returnTypes[i] = returnType
		if outInfo != nil {
			if resolver.out != nil {
				return nil, fmt.Errorf("more than one result[%d] '%s'", i, out)
			}
			resolver.out = out
			resolver.outInfo = outInfo
		}
	}

	resolver.argBuilders = argumentBuilders
	resolver.resultBuilders = returnTypes
	resolveFnValue := reflect.ValueOf(resolve)
	resolver.fnPrototype = resolveFnValue
	resolver.fn = func(p graphql.ResolveParams) (interface{}, context.Context, error) {
		args, err := resolver.buildArgs(p)
		if err != nil {
			return nil, p.Context, err
		}
		results := resolveFnValue.Call(args)
		return resolver.buildResults(p.Context, results)
	}

	return &resolver, nil
}
