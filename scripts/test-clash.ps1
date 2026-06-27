$tok = Get-Content "e:\project\safelink-ssh\configs\sub_token.txt"
$url = "http://127.0.0.1:9090/sub/${tok}?format=clash"
$resp = Invoke-WebRequest -Uri $url -UseBasicParsing
Write-Host $resp.Content
