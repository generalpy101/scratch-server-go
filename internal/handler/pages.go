package handler

import "fmt"

const HTML404 = `
<html>
<body>
	<div style="position: absolute;left: 50%;top: 50%;-webkit-transform: translate(-50%, -50%);transform: translate(-50%, -50%);color: red">
		<h1>404 Not Found</h1>
	</div>
</body>
</html>
`

const HTML500 = `
<html>
<body>
	<div style="position: absolute;left: 50%%;top: 50%% ;-webkit-transform: translate(-50%%, -50%%);transform: translate(-50%%, -50%%);color: red">
		<h1>500 Server Error</h1>
		<pre>%s</pre>
	</div>
</body>
</html>
`

const HTML400 = `
<html>
<body>
	<div style="position: absolute;left: 50%%;top: 50%% ;-webkit-transform: translate(-50%%, -50%%);transform: translate(-50%%, -50%%);color: red">
		<h1>400 Method Error</h1>
		<pre>%s</pre>
	</div>
</body>
</html>
`

func Return404() string {
	return HTML404
}

func Return500(err error) string {
	return fmt.Sprintf(HTML500, err.Error())
}

func Return400(err string) string {
	return fmt.Sprintf(HTML400, err)
}
