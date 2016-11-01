if exists('g:loaded_vigor')
  finish
endif
let g:loaded_vigor = 1

function! s:RequireVigor(host) abort
    "return jobstart(['/Users/gary/bin/vigorx'], {'rpc': v:true})
    return jobstart(['vigor'], {'rpc': v:true})
endfunction

call remote#host#Register('vigor', 'x', function('s:RequireVigor'))
call remote#host#RegisterPlugin('vigor', '0', [
\ {'type': 'autocmd', 'name': 'BufReadCmd', 'sync': 1, 'opts': {'eval': '{''Env'': {''GOROOT'': $GOROOT, ''GOPATH'': $GOPATH, ''GOOS'': $GOOS, ''GOARCH'': $GOARCH}, ''Cwd'': getcwd(), ''Name'': expand(''%''), ''Bufnr'': bufnr(''%'')}', 'pattern': 'godoc://**'}},
\ {'type': 'command', 'name': 'Fmt', 'sync': 1, 'opts': {'eval': '{''Env'': {''GOROOT'': $GOROOT, ''GOPATH'': $GOPATH, ''GOOS'': $GOOS, ''GOARCH'': $GOARCH}, ''Bufnr'': bufnr(''%'')}', 'range': '%'}},
\ {'type': 'command', 'name': 'Godef', 'sync': 1, 'opts': {'complete': 'customlist,QQQDocComplete', 'eval': '{''Env'': {''GOROOT'': $GOROOT, ''GOPATH'': $GOPATH, ''GOOS'': $GOOS, ''GOARCH'': $GOARCH}, ''Cwd'': getcwd(), ''Bufnr'': bufnr(''%'')}', 'nargs': '*'}},
\ {'type': 'command', 'name': 'Godoc', 'sync': 1, 'opts': {'complete': 'customlist,QQQDocComplete', 'eval': '{''Env'': {''GOROOT'': $GOROOT, ''GOPATH'': $GOPATH, ''GOOS'': $GOOS, ''GOARCH'': $GOARCH}, ''Cwd'': getcwd(), ''Name'': expand(''%''), ''Bufnr'': bufnr(''%'')}', 'nargs': '*'}},
\ {'type': 'function', 'name': 'QQQDocComplete', 'sync': 1, 'opts': {'eval': '{''Env'': {''GOROOT'': $GOROOT, ''GOPATH'': $GOPATH, ''GOOS'': $GOOS, ''GOARCH'': $GOARCH}, ''Cwd'': getcwd(), ''Bufnr'': bufnr(''%'')}'}},
\ ])

" vim:ts=4:sw=4:et
