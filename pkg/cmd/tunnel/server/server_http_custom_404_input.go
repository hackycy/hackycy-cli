package server

type serverHTTPCustom404PageInput struct {
	Content string
}

func (input *serverHTTPCustom404PageInput) UnmarshalJSON(source []byte) error {
	object, err := serverTunnelJSONObject(source, "content")
	if err != nil {
		return err
	}
	content, err := serverTunnelRequiredString(object, "content")
	if err != nil {
		return err
	}
	input.Content = content
	return nil
}
