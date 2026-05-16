package mailer

import "fmt"

// Layout wraps content in the RideGo email shell.
// Warm off-white bg · thin orange top accent · clean wordmark · generous whitespace.
func Layout(content string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8"/>
  <meta name="viewport" content="width=device-width,initial-scale=1.0"/>
  <title>RideGo</title>
</head>
<body style="margin:0;padding:0;background-color:#FAF9F7;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI','Helvetica Neue',Arial,sans-serif;">
  <table width="100%%" cellpadding="0" cellspacing="0" border="0" style="background-color:#FAF9F7;padding:40px 16px;">
    <tr><td align="center">
      <table width="520" cellpadding="0" cellspacing="0" border="0" style="max-width:520px;width:100%%;">

        <!-- orange accent bar -->
        <tr><td style="background-color:#F97316;height:4px;border-radius:4px 4px 0 0;font-size:0;line-height:0;">&nbsp;</td></tr>

        <!-- card body -->
        <tr><td style="background-color:#ffffff;padding:40px 48px 32px;border-left:1px solid #EEECE8;border-right:1px solid #EEECE8;">
          <p style="margin:0 0 36px;font-size:15px;font-weight:700;color:#111827;letter-spacing:0.3px;">RideGo</p>
          %s
        </td></tr>

        <!-- footer -->
        <tr><td style="background-color:#ffffff;padding:0 48px 36px;border-left:1px solid #EEECE8;border-right:1px solid #EEECE8;border-bottom:1px solid #EEECE8;border-radius:0 0 4px 4px;">
          <hr style="border:none;border-top:1px solid #F0EDE8;margin:0 0 24px;"/>
          <p style="margin:0;font-size:12px;color:#B0A898;line-height:1.8;">
            Automated message from RideGo &mdash; please do not reply.<br/>
            Made with &#10084; by Mulukutla Sai Krishna
          </p>
        </td></tr>

      </table>
    </td></tr>
  </table>
</body>
</html>`, content)
}

func WelcomeRider(name string) string {
	return Layout(fmt.Sprintf(`
<h1 style="margin:0 0 12px;font-size:28px;font-weight:700;color:#111827;line-height:1.2;">Welcome, %s.</h1>
<p style="margin:0 0 28px;font-size:15px;color:#6B7280;line-height:1.75;">
  Your RideGo account is ready. Open the app, set your destination, and your ride is on its way.
</p>
<p style="margin:0;font-size:13px;color:#C9BFB4;">Didn't create this account? Contact us immediately.</p>`, name))
}

func WelcomeDriver(name string) string {
	return Layout(fmt.Sprintf(`
<h1 style="margin:0 0 12px;font-size:28px;font-weight:700;color:#111827;line-height:1.2;">Welcome, %s.</h1>
<p style="margin:0 0 28px;font-size:15px;color:#6B7280;line-height:1.75;">
  Your RideGo driver account is ready. Go online, accept your first ride, and start earning today.
</p>
<p style="margin:0;font-size:13px;color:#C9BFB4;">Didn't create this account? Contact us immediately.</p>`, name))
}

func LoginOTP(otp string) string {
	return Layout(fmt.Sprintf(`
<h1 style="margin:0 0 8px;font-size:28px;font-weight:700;color:#111827;line-height:1.2;">Your login code</h1>
<p style="margin:0 0 32px;font-size:15px;color:#6B7280;line-height:1.75;">
  Expires in <strong style="color:#111827;">5 minutes</strong>. Do not share this with anyone.
</p>
<table cellpadding="0" cellspacing="0" border="0" width="100%%" style="margin-bottom:32px;">
  <tr>
    <td style="background:#FFF7ED;border-left:4px solid #F97316;border-radius:0 6px 6px 0;padding:20px 28px;">
      <span style="font-size:44px;font-weight:800;letter-spacing:16px;color:#111827;font-family:'Courier New',Courier,monospace;">%s</span>
    </td>
  </tr>
</table>
<p style="margin:0;font-size:13px;color:#C9BFB4;">Didn't request this? You can safely ignore this email.</p>`, otp))
}

func TripCompleted(fare float64) string {
	return Layout(fmt.Sprintf(`
<h1 style="margin:0 0 8px;font-size:28px;font-weight:700;color:#111827;line-height:1.2;">Trip completed.</h1>
<p style="margin:0 0 32px;font-size:15px;color:#6B7280;line-height:1.75;">
  Thanks for riding with RideGo. Here's your fare summary.
</p>
<table cellpadding="0" cellspacing="0" border="0" width="100%%" style="margin-bottom:32px;">
  <tr>
    <td style="background:#FFF7ED;border-left:4px solid #F97316;border-radius:0 6px 6px 0;padding:20px 28px;">
      <p style="margin:0 0 4px;font-size:11px;font-weight:600;color:#F97316;letter-spacing:1.5px;text-transform:uppercase;">Total fare</p>
      <p style="margin:0;font-size:44px;font-weight:800;color:#111827;letter-spacing:-1px;line-height:1;">&#8377;%.2f</p>
    </td>
  </tr>
</table>
<p style="margin:0;font-size:13px;color:#C9BFB4;">See you on the next ride.</p>`, fare))
}

func TripCancelled() string {
	return Layout(`
<h1 style="margin:0 0 8px;font-size:28px;font-weight:700;color:#111827;line-height:1.2;">Trip cancelled.</h1>
<p style="margin:0 0 28px;font-size:15px;color:#6B7280;line-height:1.75;">
  Your trip has been cancelled and no charge has been applied to your account.
</p>
<p style="margin:0;font-size:13px;color:#C9BFB4;">Didn't cancel this? Reach out to our support team.</p>`)
}

func PaymentCompleted(amount float64, tripID string) string {
	return Layout(fmt.Sprintf(`
<h1 style="margin:0 0 8px;font-size:28px;font-weight:700;color:#111827;line-height:1.2;">Payment confirmed.</h1>
<p style="margin:0 0 32px;font-size:15px;color:#6B7280;line-height:1.75;">
  Your payment was processed successfully.
</p>
<table cellpadding="0" cellspacing="0" border="0" width="100%%" style="margin-bottom:32px;">
  <tr>
    <td style="background:#FFF7ED;border-left:4px solid #F97316;border-radius:0 6px 6px 0;padding:20px 28px;">
      <p style="margin:0 0 4px;font-size:11px;font-weight:600;color:#F97316;letter-spacing:1.5px;text-transform:uppercase;">Amount paid</p>
      <p style="margin:0 0 8px;font-size:44px;font-weight:800;color:#111827;letter-spacing:-1px;line-height:1;">&#8377;%.2f</p>
      <p style="margin:0;font-size:12px;color:#B0A898;">Trip %s</p>
    </td>
  </tr>
</table>
<p style="margin:0;font-size:13px;color:#C9BFB4;">Thank you for using RideGo.</p>`, amount, tripID))
}
