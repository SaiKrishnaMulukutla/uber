package controllers

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// CheckoutWS upgrades to WebSocket and waits for payment completion signal.
func (h *Handler) CheckoutWS(w http.ResponseWriter, r *http.Request) {
	paymentID := chi.URLParam(r, "id")
	h.hub.HandleWS(w, r, paymentID)
}

// Checkout serves the payment page for a given payment ID.
func (h *Handler) Checkout(w http.ResponseWriter, r *http.Request) {
	paymentID := chi.URLParam(r, "id")
	token := r.URL.Query().Get("token")

	payment, err := h.svc.GetByPaymentID(r.Context(), paymentID)
	if err != nil || payment == nil {
		http.Error(w, "payment not found", http.StatusNotFound)
		return
	}

	if payment.Status == "COMPLETED" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, renderSuccess(payment.Amount, payment.PaymentMethod, payment.TripID))
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, renderCheckout(
		paymentID, token, payment.Amount, payment.TripID,
		payment.ProviderOrderID, h.keyID, payment.PaymentMethod,
	))
}

func renderCheckout(paymentID, token string, amount float64, tripID, providerOrderID, keyID, paymentMethod string) string {
	wsPath := "/payments/ws/" + paymentID
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8"/>
<meta name="viewport" content="width=device-width,initial-scale=1"/>
<title>Uber — Payment</title>
<script src="https://checkout.razorpay.com/v1/checkout.js"></script>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{background:#f5f5f5;font-family:'Helvetica Neue',Helvetica,Arial,sans-serif;min-height:100vh;display:flex;align-items:center;justify-content:center}
.card{background:#fff;border-radius:8px;box-shadow:0 4px 24px rgba(0,0,0,.12);width:100%%;max-width:440px;overflow:hidden}
.header{background:#000;padding:24px 28px;display:flex;align-items:center}
.logo{color:#fff;font-size:24px;font-weight:700;letter-spacing:-.5px}
.fare{color:#fff;font-size:22px;font-weight:700}
.label{color:#888;font-size:12px;margin-top:2px}
.tabs{display:flex;border-bottom:1px solid #eee}
.tab{flex:1;padding:14px;text-align:center;font-size:14px;font-weight:600;cursor:pointer;color:#888;border-bottom:3px solid transparent;transition:all .2s}
.tab.active{color:#000;border-color:#000}
.tab:hover:not(.active){color:#333}
.body{padding:28px}
.panel{display:none}.panel.active{display:block}

/* Cash */
.cash-icon{text-align:center;font-size:48px;margin-bottom:16px}
.cash-amount{text-align:center;font-size:32px;font-weight:700;margin-bottom:6px}
.cash-sub{text-align:center;font-size:13px;color:#888;margin-bottom:16px}
.cash-steps{list-style:none;text-align:left;margin:0 0 20px;padding:0}
.cash-steps li{display:flex;align-items:flex-start;gap:10px;padding:8px 0;font-size:13px;color:#555;border-bottom:1px solid #f0f0f0}
.cash-steps li:last-child{border:none}
.step-num{background:#000;color:#fff;border-radius:50%%;width:20px;height:20px;display:flex;align-items:center;justify-content:center;font-size:11px;font-weight:700;flex-shrink:0;margin-top:1px}
.pulse-ring{width:64px;height:64px;border:3px solid #000;border-radius:50%%;margin:0 auto 16px;animation:pulse 1.4s ease-in-out infinite}
@keyframes pulse{0%%,100%%{opacity:1;transform:scale(1)}50%%{opacity:.4;transform:scale(1.08)}}

/* UPI */
.upi-app-row{display:flex;gap:10px;justify-content:center;margin-bottom:20px}
.upi-app{display:flex;flex-direction:column;align-items:center;gap:4px;font-size:10px;font-weight:600;color:#555}
.upi-app-icon{width:44px;height:44px;border-radius:12px;display:flex;align-items:center;justify-content:center;font-size:18px;font-weight:900}
.gpay{background:#fff;border:1.5px solid #eee;color:#4285f4;font-size:11px;font-weight:900;letter-spacing:-.5px}
.phonepe{background:#5f259f;color:#fff}
.paytm{background:#00baf2;color:#fff;font-size:10px;font-weight:900}
.bhim{background:#00529b;color:#fff;font-size:11px;font-weight:900}
.upi-sub{text-align:center;font-size:13px;color:#555;margin-bottom:20px;line-height:1.6}

/* Card */
.card-logos{display:flex;gap:8px;margin-bottom:20px}
.card-logo{background:#f5f5f5;border-radius:4px;padding:6px 10px;font-size:11px;font-weight:700;color:#555}

/* Button */
.btn{width:100%%;padding:15px;background:#000;color:#fff;border:none;border-radius:6px;font-size:16px;font-weight:700;cursor:pointer;transition:background .2s}
.btn:hover{background:#222}
.btn:disabled{background:#999;cursor:not-allowed}

/* Success overlay */
.success{text-align:center;padding:8px 0}
.success-icon{font-size:60px;margin-bottom:16px}
.success-title{font-size:22px;font-weight:700;margin-bottom:8px}
.success-sub{font-size:14px;color:#555;margin-bottom:24px}
.success-detail{background:#f5f5f5;border-radius:6px;padding:16px;text-align:left;margin-bottom:24px}
.detail-row{display:flex;justify-content:space-between;font-size:13px;padding:4px 0}
.detail-row .k{color:#888}.detail-row .v{font-weight:600}
.back-btn{padding:12px 32px;background:#000;color:#fff;border-radius:6px;font-size:14px;font-weight:600;border:none;cursor:pointer}
</style>
</head>
<body>
<div class="card">
  <div class="header">
    <div>
      <div class="logo">Uber</div>
      <div class="label">Trip Payment</div>
    </div>
    <div style="margin-left:auto;text-align:right">
      <div class="fare">&#8377;%.2f</div>
      <div class="label">Amount due</div>
    </div>
  </div>

  <div class="tabs">
    <div class="tab" onclick="switchTab('cash')">&#128181; Cash</div>
    <div class="tab" onclick="switchTab('upi')">&#128242; UPI</div>
    <div class="tab" onclick="switchTab('card')">&#128179; Card</div>
  </div>

  <div class="body">

    <!-- CASH -->
    <div id="panel-cash" class="panel">
      <div class="cash-icon">&#128181;</div>
      <div class="cash-amount">&#8377;%.2f</div>
      <div class="cash-sub">Pay your driver directly in cash</div>
      <ul class="cash-steps">
        <li><span class="step-num">1</span>Hand <strong>&#8377;%.2f</strong> in cash to your driver</li>
        <li><span class="step-num">2</span>Driver confirms receipt on their app</li>
        <li><span class="step-num">3</span>This page updates automatically</li>
      </ul>
      <div style="text-align:center">
        <div class="pulse-ring"></div>
        <div style="font-size:14px;font-weight:700">Waiting for driver confirmation</div>
        <div style="font-size:12px;color:#888;margin-top:6px">Page will update once driver confirms</div>
      </div>
    </div>

    <!-- UPI -->
    <div id="panel-upi" class="panel">
      <div class="upi-app-row">
        <div class="upi-app"><div class="upi-app-icon gpay">G</div><span>GPay</span></div>
        <div class="upi-app"><div class="upi-app-icon phonepe">&#9654;</div><span>PhonePe</span></div>
        <div class="upi-app"><div class="upi-app-icon paytm">Pay</div><span>Paytm</span></div>
        <div class="upi-app"><div class="upi-app-icon bhim">B</div><span>BHIM</span></div>
      </div>
      <div class="upi-sub">
        Pay securely via any UPI app.<br/>
        You'll enter your UPI ID in the Razorpay payment screen.
      </div>
      <button class="btn" id="btn-upi" onclick="payUPI()">Pay &#8377;%.2f via UPI</button>
    </div>

    <!-- CARD -->
    <div id="panel-card" class="panel">
      <div class="card-logos">
        <div class="card-logo">VISA</div>
        <div class="card-logo">MC</div>
        <div class="card-logo">AMEX</div>
        <div class="card-logo">RuPay</div>
      </div>
      <div style="font-size:13px;color:#555;margin-bottom:20px;line-height:1.6">
        Securely pay via Razorpay. Your card details are never stored on our servers.
        <br/><br/>
        <strong>Test card:</strong> 4100 2800 0000 1007 &nbsp;&middot;&nbsp; Any future expiry &nbsp;&middot;&nbsp; Any CVV
      </div>
      <button class="btn" id="btn-card" onclick="payCard()">Pay &#8377;%.2f with Card</button>
    </div>

  </div>
</div>

<script>
const PAYMENT_ID = %q;
const TOKEN      = %q;
const TRIP_ID    = %q;
const AMOUNT     = %.2f;
const RZP_KEY    = %q;
const RZP_ORDER  = %q;
const WS_PATH    = %q;
const METHOD     = %q;

const wsProto = location.protocol === 'https:' ? 'wss:' : 'ws:';
const ws = new WebSocket(wsProto + '//' + location.host + WS_PATH + '?token=' + TOKEN);
ws.onmessage = function(e) {
  var d = JSON.parse(e.data);
  if (d.status === 'completed') showSuccess('online', AMOUNT);
};

function switchTab(tab) {
  document.querySelectorAll('.tab').forEach(function(t, i) {
    var names = ['cash','upi','card'];
    t.classList.toggle('active', names[i] === tab);
  });
  document.querySelectorAll('.panel').forEach(function(p) { p.classList.remove('active'); });
  document.getElementById('panel-' + tab).classList.add('active');
}

switchTab((METHOD === 'cash' || METHOD === 'card') ? METHOD : 'upi');

function openRazorpay(prefillMethod, btnID) {
  var btn = document.getElementById(btnID);
  btn.disabled = true;
  var options = {
    key: RZP_KEY, amount: Math.round(AMOUNT * 100), currency: 'INR',
    order_id: RZP_ORDER, name: 'Uber', description: 'Trip Payment',
    prefill: {method: prefillMethod},
    theme: {color: '#000000'},
    modal: {ondismiss: function() { btn.disabled = false; }},
    handler: async function(resp) {
      try {
        var r = await fetch('/payments/verify', {
          method: 'POST',
          headers: {'Content-Type':'application/json','Authorization':'Bearer '+TOKEN},
          body: JSON.stringify({
            payment_id: PAYMENT_ID,
            provider_order_id: resp.razorpay_order_id,
            provider_payment_id: resp.razorpay_payment_id,
            signature: resp.razorpay_signature
          })
        });
        var d = await r.json();
        if (d.error) throw new Error(d.error);
        showSuccess(prefillMethod, AMOUNT);
      } catch(e) { btn.disabled = false; alert('Verification failed: ' + e.message); }
    }
  };
  new Razorpay(options).open();
}

function payUPI()  { openRazorpay('upi',  'btn-upi');  }
function payCard() { openRazorpay('card', 'btn-card'); }

function showSuccess(method, amount) {
  ws.close();
  var labels = {cash:'Cash',card:'Credit/Debit Card',upi:'UPI',online:'Online'};
  document.querySelector('.tabs').style.display = 'none';
  document.querySelector('.body').innerHTML =
    '<div class="success">' +
    '<div class="success-icon">&#10004;&#65039;</div>' +
    '<div class="success-title">Payment Successful</div>' +
    '<div class="success-sub">Your trip payment has been confirmed.</div>' +
    '<div class="success-detail">' +
    '<div class="detail-row"><span class="k">Amount Paid</span><span class="v">&#8377;' + (+amount).toFixed(2) + '</span></div>' +
    '<div class="detail-row"><span class="k">Method</span><span class="v">' + (labels[method]||method) + '</span></div>' +
    '<div class="detail-row"><span class="k">Trip ID</span><span class="v" style="font-size:11px">' + TRIP_ID.substring(0,8) + '...</span></div>' +
    '<div class="detail-row"><span class="k">Status</span><span class="v" style="color:#2e7d32">&#10003; COMPLETED</span></div>' +
    '</div>' +
    '<button class="back-btn" onclick="window.close()">Done</button>' +
    '</div>';
}
</script>
</body>
</html>`,
		amount,   // header fare
		amount,   // cash tab amount
		amount,   // cash steps "Hand ₹X"
		amount,   // UPI pay button
		amount,   // card pay button
		paymentID, token, tripID, amount, keyID, providerOrderID, wsPath, paymentMethod)
}

func renderSuccess(amount float64, method, tripID string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en"><head><meta charset="UTF-8"/><title>Uber — Payment Complete</title>
<style>*{box-sizing:border-box;margin:0;padding:0}body{background:#f5f5f5;font-family:'Helvetica Neue',Helvetica,Arial,sans-serif;min-height:100vh;display:flex;align-items:center;justify-content:center}.card{background:#fff;border-radius:8px;box-shadow:0 4px 24px rgba(0,0,0,.12);width:100%%;max-width:440px;overflow:hidden}.header{background:#000;padding:24px 28px}.logo{color:#fff;font-size:24px;font-weight:700}.body{padding:36px 28px;text-align:center}.icon{font-size:60px;margin-bottom:16px}.title{font-size:22px;font-weight:700;margin-bottom:8px}.sub{font-size:14px;color:#555;margin-bottom:24px}.detail{background:#f5f5f5;border-radius:6px;padding:16px;text-align:left;margin-bottom:24px}.row{display:flex;justify-content:space-between;font-size:13px;padding:4px 0}.k{color:#888}.v{font-weight:600}.btn{padding:12px 32px;background:#000;color:#fff;border-radius:6px;font-size:14px;font-weight:600;border:none;cursor:pointer}</style>
</head><body>
<div class="card">
  <div class="header"><div class="logo">Uber</div></div>
  <div class="body">
    <div class="icon">&#10004;&#65039;</div>
    <div class="title">Payment Successful</div>
    <div class="sub">Your trip payment has been confirmed.</div>
    <div class="detail">
      <div class="row"><span class="k">Amount Paid</span><span class="v">&#8377;%.2f</span></div>
      <div class="row"><span class="k">Method</span><span class="v">%s</span></div>
      <div class="row"><span class="k">Trip ID</span><span class="v" style="font-size:11px">%s</span></div>
      <div class="row"><span class="k">Status</span><span class="v" style="color:#2e7d32">&#10003; COMPLETED</span></div>
    </div>
    <button class="btn" onclick="window.close()">Done</button>
  </div>
</div>
</body></html>`, amount, method, tripID)
}
