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

// Checkout serves the Uber-branded payment page for a given payment ID.
func (h *Handler) Checkout(w http.ResponseWriter, r *http.Request) {
	paymentID := chi.URLParam(r, "id")
	token := r.URL.Query().Get("token")

	payment, err := h.svc.GetByPaymentID(r.Context(), paymentID)
	if err != nil || payment == nil {
		http.Error(w, "payment not found", http.StatusNotFound)
		return
	}

	// Already completed — show success directly
	if payment.Status == "COMPLETED" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, renderSuccess(payment.Amount, payment.PaymentMethod, payment.TripID))
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, renderCheckout(paymentID, token, payment.Amount, payment.TripID, payment.ProviderOrderID, payment.ProviderPaymentID))
}

// CheckoutCash handles cash payment confirmation.
func (h *Handler) CheckoutCash(w http.ResponseWriter, r *http.Request) {
	paymentID := chi.URLParam(r, "id")
	payment, err := h.svc.SimulateSuccess(r.Context(), paymentID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": payment.Status, "amount": payment.Amount})
}

// CheckoutUPI handles simulated UPI payment.
func (h *Handler) CheckoutUPI(w http.ResponseWriter, r *http.Request) {
	paymentID := chi.URLParam(r, "id")
	payment, err := h.svc.SimulateSuccess(r.Context(), paymentID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": payment.Status, "amount": payment.Amount})
}

func renderCheckout(paymentID, token string, amount float64, tripID, providerOrderID, keyID string) string {
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
.header{background:#000;padding:24px 28px;display:flex;align-items:center;gap:12px}
.header .logo{color:#fff;font-size:24px;font-weight:700;letter-spacing:-.5px}
.header .fare{margin-left:auto;color:#fff;font-size:22px;font-weight:700}
.header .label{color:#888;font-size:12px;margin-top:2px}
.tabs{display:flex;border-bottom:1px solid #eee}
.tab{flex:1;padding:14px;text-align:center;font-size:14px;font-weight:600;cursor:pointer;color:#888;border-bottom:3px solid transparent;transition:all .2s}
.tab.active{color:#000;border-color:#000}
.tab:hover:not(.active){color:#333}
.body{padding:28px}
.panel{display:none}.panel.active{display:block}

/* Cash */
.cash-icon{text-align:center;font-size:48px;margin-bottom:16px}
.cash-note{font-size:14px;color:#555;line-height:1.6;text-align:center;margin-bottom:24px}
.cash-amount{text-align:center;font-size:32px;font-weight:700;margin-bottom:8px}
.cash-sub{text-align:center;font-size:13px;color:#888;margin-bottom:28px}

/* UPI */
.upi-logos{display:flex;gap:12px;justify-content:center;margin-bottom:20px}
.upi-logo{background:#f5f5f5;border-radius:8px;padding:8px 14px;font-size:12px;font-weight:700;color:#555}
.input-wrap{position:relative;margin-bottom:20px}
.input-wrap input{width:100%%;padding:14px 16px;border:1.5px solid #ddd;border-radius:6px;font-size:15px;outline:none;transition:border .2s}
.input-wrap input:focus{border-color:#000}
.input-wrap label{position:absolute;top:-9px;left:12px;background:#fff;padding:0 4px;font-size:11px;color:#888;font-weight:600;text-transform:uppercase;letter-spacing:.5px}

/* Card */
.card-logos{display:flex;gap:8px;margin-bottom:20px}
.card-logo{background:#f5f5f5;border-radius:4px;padding:6px 10px;font-size:11px;font-weight:700;color:#555}

/* Button */
.btn{width:100%%;padding:15px;background:#000;color:#fff;border:none;border-radius:6px;font-size:16px;font-weight:700;cursor:pointer;transition:background .2s;position:relative}
.btn:hover{background:#222}
.btn:disabled{background:#999;cursor:not-allowed}
.spinner{display:none;width:18px;height:18px;border:2px solid #fff;border-top-color:transparent;border-radius:50%%;animation:spin .7s linear infinite;margin:0 auto}
@keyframes spin{to{transform:rotate(360deg)}}

/* Success */
.success{text-align:center;padding:20px 0}
.success-icon{font-size:60px;margin-bottom:16px}
.success-title{font-size:22px;font-weight:700;margin-bottom:8px}
.success-sub{font-size:14px;color:#555;margin-bottom:24px}
.success-detail{background:#f5f5f5;border-radius:6px;padding:16px;text-align:left;margin-bottom:24px}
.detail-row{display:flex;justify-content:space-between;font-size:13px;padding:4px 0}
.detail-row .k{color:#888}.detail-row .v{font-weight:600}
.back-btn{display:inline-block;padding:12px 32px;background:#000;color:#fff;border-radius:6px;font-size:14px;font-weight:600;text-decoration:none;cursor:pointer;border:none}
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
      <div class="fare">₹%.2f</div>
      <div class="label">Amount due</div>
    </div>
  </div>

  <div class="tabs">
    <div class="tab active" onclick="switchTab('cash')">💵 Cash</div>
    <div class="tab" onclick="switchTab('card')">💳 Card</div>
    <div class="tab" onclick="switchTab('upi')">📱 UPI</div>
  </div>

  <div class="body">
    <!-- CASH -->
    <div id="panel-cash" class="panel active">
      <div class="cash-icon">💵</div>
      <div class="cash-amount">₹%.2f</div>
      <div class="cash-sub">Pay your driver directly in cash</div>
      <div class="cash-note">No online transaction needed. Hand the cash to your driver at the end of the trip. Your trip receipt will be generated automatically.</div>
      <br/>
      <button class="btn" id="btn-cash" onclick="payCash()">Confirm Cash Payment</button>
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
        <strong>Test card:</strong> 4111 1111 1111 1111 &nbsp;·&nbsp; Any future expiry &nbsp;·&nbsp; Any CVV
      </div>
      <button class="btn" id="btn-card" onclick="payCard()">Pay ₹%.2f with Card</button>
    </div>

    <!-- UPI -->
    <div id="panel-upi" class="panel">
      <div class="upi-logos">
        <div class="upi-logo">GPay</div>
        <div class="upi-logo">PhonePe</div>
        <div class="upi-logo">Paytm</div>
        <div class="upi-logo">BHIM</div>
      </div>
      <div class="input-wrap">
        <label>UPI ID</label>
        <input id="upi-id" type="text" placeholder="yourname@upi"/>
      </div>
      <button class="btn" id="btn-upi" onclick="payUPI()">Pay ₹%.2f via UPI</button>
      <div id="upi-spinner" style="text-align:center;margin-top:16px;display:none">
        <div class="spinner" style="display:inline-block;border-color:#000;border-top-color:transparent"></div>
        <div style="font-size:12px;color:#888;margin-top:8px">Verifying with your UPI app...</div>
      </div>
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

// WebSocket — listen for server-push completion
const wsProto = location.protocol === 'https:' ? 'wss:' : 'ws:';
const ws = new WebSocket(wsProto + '//' + location.host + WS_PATH + '?token=' + TOKEN);
ws.onmessage = (e) => {
  const d = JSON.parse(e.data);
  if (d.status === 'completed') showSuccess('online', AMOUNT);
};

function switchTab(tab) {
  document.querySelectorAll('.tab').forEach((t,i) => {
    const names = ['cash','card','upi'];
    t.classList.toggle('active', names[i] === tab);
  });
  document.querySelectorAll('.panel').forEach(p => p.classList.remove('active'));
  document.getElementById('panel-' + tab).classList.add('active');
}

async function payCash() {
  const btn = document.getElementById('btn-cash');
  btn.disabled = true; btn.textContent = 'Processing...';
  try {
    const r = await fetch('/payments/checkout/' + PAYMENT_ID + '/cash?token=' + TOKEN, {method:'POST'});
    const d = await r.json();
    if (d.error) throw new Error(d.error);
    showSuccess('cash', d.amount);
  } catch(e) {
    btn.disabled = false; btn.textContent = 'Confirm Cash Payment';
    alert('Error: ' + e.message);
  }
}

function payCard() {
  const btn = document.getElementById('btn-card');
  btn.disabled = true;
  const options = {
    key: RZP_KEY,
    amount: Math.round(AMOUNT * 100),
    currency: 'INR',
    order_id: RZP_ORDER,
    name: 'Uber',
    description: 'Trip Payment',
    image: 'data:image/svg+xml;base64,PHN2ZyB3aWR0aD0iNDAiIGhlaWdodD0iNDAiIHZpZXdCb3g9IjAgMCA0MCA0MCIgZmlsbD0ibm9uZSIgeG1sbnM9Imh0dHA6Ly93d3cudzMub3JnLzIwMDAvc3ZnIj48cmVjdCB3aWR0aD0iNDAiIGhlaWdodD0iNDAiIHJ4PSI4IiBmaWxsPSIjMDAwIi8+PHRleHQgeD0iNTAlJSIgeT0iNTUlJSIgZG9taW5hbnQtYmFzZWxpbmU9Im1pZGRsZSIgdGV4dC1hbmNob3I9Im1pZGRsZSIgZmlsbD0iI2ZmZiIgZm9udC1mYW1pbHk9IkhlbHZldGljYSIgZm9udC13ZWlnaHQ9IjcwMCIgZm9udC1zaXplPSIxOCI+VTwvdGV4dD48L3N2Zz4=',
    theme: {color: '#000000'},
    modal: {ondismiss: () => { btn.disabled = false; }},
    handler: async (resp) => {
      try {
        const r = await fetch('/payments/verify', {
          method: 'POST',
          headers: {'Content-Type':'application/json','Authorization':'Bearer '+TOKEN},
          body: JSON.stringify({
            payment_id: PAYMENT_ID,
            provider_order_id: resp.razorpay_order_id,
            provider_payment_id: resp.razorpay_payment_id,
            signature: resp.razorpay_signature
          })
        });
        const d = await r.json();
        if (d.error) throw new Error(d.error);
        showSuccess('card', AMOUNT);
      } catch(e) {
        btn.disabled = false;
        alert('Verification failed: ' + e.message);
      }
    }
  };
  new Razorpay(options).open();
}

async function payUPI() {
  const upiId = document.getElementById('upi-id').value.trim();
  if (!upiId || !upiId.includes('@')) { alert('Enter a valid UPI ID (e.g. name@upi)'); return; }
  const btn = document.getElementById('btn-upi');
  btn.disabled = true;
  document.getElementById('upi-spinner').style.display = 'block';
  await new Promise(r => setTimeout(r, 2000)); // simulate UPI processing
  try {
    const r = await fetch('/payments/checkout/' + PAYMENT_ID + '/upi?token=' + TOKEN, {method:'POST'});
    const d = await r.json();
    if (d.error) throw new Error(d.error);
    showSuccess('upi', d.amount);
  } catch(e) {
    btn.disabled = false;
    document.getElementById('upi-spinner').style.display = 'none';
    alert('Error: ' + e.message);
  }
}

function showSuccess(method, amount) {
  ws.close();
  const methodLabels = {cash:'Cash',card:'Credit/Debit Card',upi:'UPI',online:'Online'};
  document.querySelector('.body').innerHTML =
    '<div class="success">' +
    '<div class="success-icon">\u2705</div>' +
    '<div class="success-title">Payment Successful</div>' +
    '<div class="success-sub">Your trip payment has been confirmed.</div>' +
    '<div class="success-detail">' +
    '<div class="detail-row"><span class="k">Amount Paid</span><span class="v">\u20b9' + amount.toFixed(2) + '</span></div>' +
    '<div class="detail-row"><span class="k">Payment Method</span><span class="v">' + (methodLabels[method]||method) + '</span></div>' +
    '<div class="detail-row"><span class="k">Trip ID</span><span class="v" style="font-size:11px">' + TRIP_ID + '</span></div>' +
    '<div class="detail-row"><span class="k">Status</span><span class="v" style="color:#2e7d32">COMPLETED</span></div>' +
    '</div>' +
    '<button class="back-btn" onclick="window.close()">Close</button>' +
    '</div>';
  document.querySelector('.tabs').style.display = 'none';
}
</script>
</body>
</html>`,
		amount, amount, amount, amount, amount,
		paymentID, token, tripID, amount, keyID, providerOrderID, wsPath)
}

func renderSuccess(amount float64, method, tripID string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en"><head><meta charset="UTF-8"/><title>Uber — Payment Complete</title>
<style>*{box-sizing:border-box;margin:0;padding:0}body{background:#f5f5f5;font-family:'Helvetica Neue',Helvetica,Arial,sans-serif;min-height:100vh;display:flex;align-items:center;justify-content:center}.card{background:#fff;border-radius:8px;box-shadow:0 4px 24px rgba(0,0,0,.12);width:100%%;max-width:440px;overflow:hidden}.header{background:#000;padding:24px 28px}.logo{color:#fff;font-size:24px;font-weight:700}.body{padding:36px 28px;text-align:center}.icon{font-size:60px;margin-bottom:16px}.title{font-size:22px;font-weight:700;margin-bottom:8px}.sub{font-size:14px;color:#555;margin-bottom:24px}.detail{background:#f5f5f5;border-radius:6px;padding:16px;text-align:left;margin-bottom:24px}.row{display:flex;justify-content:space-between;font-size:13px;padding:4px 0}.k{color:#888}.v{font-weight:600}.btn{padding:12px 32px;background:#000;color:#fff;border-radius:6px;font-size:14px;font-weight:600;border:none;cursor:pointer}</style>
</head><body>
<div class="card">
  <div class="header"><div class="logo">Uber</div></div>
  <div class="body">
    <div class="icon">✅</div>
    <div class="title">Payment Successful</div>
    <div class="sub">Your trip payment has been confirmed.</div>
    <div class="detail">
      <div class="row"><span class="k">Amount Paid</span><span class="v">₹%.2f</span></div>
      <div class="row"><span class="k">Payment Method</span><span class="v">%s</span></div>
      <div class="row"><span class="k">Trip ID</span><span class="v" style="font-size:11px">%s</span></div>
      <div class="row"><span class="k">Status</span><span class="v" style="color:#2e7d32">COMPLETED</span></div>
    </div>
    <button class="btn" onclick="window.close()">Close</button>
  </div>
</div>
</body></html>`, amount, method, tripID)
}
